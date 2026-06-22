package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

var inlineImageRE = regexp.MustCompile(`!\[([^\]]*)\]\((data:(image/[^;,]+)(?:;base64)?,[^)]+)\)`)
var attachmentMetaRE = regexp.MustCompile(`<!--sapaloq-attachment:[A-Za-z0-9+/=]+-->`)

// transportRetryBaseBackoff is the per-attempt backoff unit for retrying a turn
// after a transient transport error (attempt N waits N×base, capped at 5s). It
// is a package var only so tests can zero it to run instantly.
var transportRetryBaseBackoff = 750 * time.Millisecond

// runConversation drives one Ask (chat) turn. If thinkingOut is non-nil,
// reasoning (EventThinkingDelta) text is accumulated into it so the caller can
// persist it as a show-only "thinking" turn — separate from the assistant
// answer (`all`). It is a thin wrapper that configures the SHARED engine
// (runTurnLoop) for the chat role: the full Ask tool surface, a live channel
// sink, no heartbeat, and natural finish on a tool-less turn.
func (o *Orchestrator) runConversation(ctx context.Context, snap providerSnapshot, out chan<- bridge.StreamEvent, sessionID, fallbackTask string, messages []bridge.Message, thinkingOut *strings.Builder) (strings.Builder, error) {
	return o.runConversationActor(ctx, snap, out, sessionID, sessionID, fallbackTask, messages, thinkingOut)
}

// runConversationActor runs the shared Ask engine under an explicit actor id.
// Foreground chat uses sessionID; invisible mediators use their own runID while
// sharing the same bounded conversation snapshot, preventing mailbox/tool-job
// identity conflicts with a concurrently active UI orchestrator.
func (o *Orchestrator) runConversationActor(ctx context.Context, snap providerSnapshot, out chan<- bridge.StreamEvent, sessionID, runID, fallbackTask string, messages []bridge.Message, thinkingOut *strings.Builder) (strings.Builder, error) {
	cfg := turnConfig{
		sessionID:       sessionID,
		runID:           runID,
		tools:           askTools,
		sink:            chatSink{o: o, out: out},
		finishOnNoTool:  true,
		thinkingOut:     thinkingOut,
		recordToolTurns: true,
		dispatch: func(ctx context.Context, call parse.ToolCall) turnOutcome {
			res := o.handleAskTool(ctx, snap, out, sessionID, fallbackTask, call)
			return turnOutcome{text: res.text, handled: res.handled, stop: res.stop}
		},
	}
	return o.runTurnLoop(ctx, snap, fallbackTask, messages, cfg)
}

// runTurnLoop is the single multi-turn inference engine behind both chat and
// every sub-agent role. The variable parts (tool surface, per-call dispatch,
// output sink, finish policy, heartbeat) are supplied via turnConfig; the
// resilience logic (wall-time/turn/tool budgets, identical-tool and no-progress
// loop detection, proactive + overflow-triggered compaction, vision downgrade,
// clean stream/error handling) is shared so a fix lands once for everyone.
func (o *Orchestrator) runTurnLoop(ctx context.Context, snap providerSnapshot, fallbackTask string, messages []bridge.Message, cfg turnConfig) (strings.Builder, error) {
	sessionID := cfg.sessionID
	runID := cfg.runID
	if runID == "" {
		runID = sessionID
	}
	out := cfg.sink
	thinkingOut := cfg.thinkingOut
	var all strings.Builder
	cleanMessages, images := extractImages(messages)
	// One-shot: once we've dropped images and retried text-only we never
	// re-attach them in this run, so a model that can't see images degrades
	// gracefully instead of looping on the same 400.
	visionDowngraded := false
	// If this model is already known text-only (marked false in a prior run or
	// in config), don't bother sending the image — drop it and proceed on the
	// text placeholder so the user still gets an answer instead of an error.
	if len(images) > 0 && !o.visionAllowed(snap.entry.Key, snap.entry.Model) {
		images = nil
		visionDowngraded = true
	}
	runtimeCfg := snap.cfg.Orchestrator.WithDefaults()
	budget := runtimeCfg.Continuation
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(budget.MaxWallTimeMinutes)*time.Minute)
	defer cancel()
	lastOutcome := ""
	noProgressTurns := 0
	lastToolSignature := ""
	identicalToolCalls := 0
	toolCalls := 0
	lastCompactedMessageCount := 0

	// Bounds how many times an upstream context-overflow 400 can trigger a
	// forced compaction + retry, so a conversation that can't shrink enough
	// surfaces the error instead of looping forever.
	forcedCompactions := 0
	const maxForcedCompactions = 3

	// Bounds consecutive retries of a turn on a transient transport error (slow
	// provider TTFB, dropped connection, 5xx/429). Reset to 0 once any turn
	// completes without a transport error, so a long run that hits the
	// occasional blip keeps going, while a provider that is genuinely down still
	// surfaces the error instead of retrying forever.
	transportRetries := 0
	const maxTransportRetries = 4

	maxInferenceTurns := budget.MaxInferenceTurns
	if cfg.maxInferenceTurns > 0 {
		maxInferenceTurns = cfg.maxInferenceTurns
	}

	for inferenceTurn := 1; inferenceTurn <= maxInferenceTurns; inferenceTurn++ {
		if err := runCtx.Err(); err != nil {
			out.emit(context.Background(), bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			if ctx.Err() != nil {
				return all, nil
			}
			return all, fmt.Errorf("continuation wall-time budget exhausted after %d minutes", budget.MaxWallTimeMinutes)
		}
		if control := actorEventsPrompt(o.drainActorEvents(runID)); control != "" {
			cleanMessages = append(cleanMessages, bridge.Message{Role: "user", Content: control})
		}
		// Heartbeat at the top of every turn so the health watchdog can tell a
		// genuinely-working agent (advancing turns) from a wedged goroutine.
		out.beat(fmt.Sprintf("inference turn %d/%d", inferenceTurn, maxInferenceTurns))
		if shouldCompactConversation(cleanMessages, snap.entry.ContextWindow, runtimeCfg.Compaction.BackgroundThreshold) &&
			len(cleanMessages) > lastCompactedMessageCount+2 {
			blocking := conversationTokenRatio(cleanMessages, snap.entry.ContextWindow) >= runtimeCfg.Compaction.BlockingThreshold
			if blocking {
				out.emit(runCtx, statusEvent(sessionID, "compacting"))
			}
			cleanMessages = compactConversationMessages(cleanMessages, fallbackTask, runtimeCfg.Compaction.PreserveRecentFraction)
			lastCompactedMessageCount = len(cleanMessages)
			if blocking {
				out.emit(runCtx, statusEvent(sessionID, "working"))
			}
			if !runtimeCfg.Compaction.ResumeAfterCompaction {
				return all, fmt.Errorf("continuation paused after compaction by configuration")
			}
		}

		out.emit(runCtx, statusEvent(sessionID, "working"))
		// Give every inference attempt its own cancellation scope. The outer
		// run may retry the same turn after a recoverable provider error, so it
		// must be able to abandon a broken stream without cancelling the whole
		// conversation. Conversely, user Stop must never depend on the bridge
		// producer noticing cancellation and closing its channel promptly.
		attemptCtx, cancelAttempt := context.WithCancel(runCtx)
		stream, err := snap.br.Complete(attemptCtx, bridge.Request{
			SessionID:     sessionID,
			Messages:      cleanMessages,
			Model:         snap.entry.Model,
			DeclaredTools: cfg.tools,
			Images:        images,
		})
		if err != nil {
			cancelAttempt()
			if runCtx.Err() != nil && ctx.Err() != nil {
				out.emit(context.Background(), bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
				return all, nil
			}
			return all, err
		}
		var response strings.Builder
		var toolResults []string
		var pendingTools []scheduledTool
		stop := false
		hadError := false
		retryTextOnly := false
		retryCompacted := false
		retryTransport := false
		lastErr := ""
	streamLoop:
		for {
			var ev bridge.StreamEvent
			select {
			case <-runCtx.Done():
				cancelAttempt()
				out.emit(context.Background(), bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
				if ctx.Err() != nil {
					return all, nil
				}
				return all, fmt.Errorf("continuation wall-time budget exhausted after %d minutes", budget.MaxWallTimeMinutes)
			case next, ok := <-stream:
				if !ok {
					break streamLoop
				}
				ev = next
			}
			if ev.SessionID == "" {
				ev.SessionID = sessionID
			}
			switch ev.Kind {
			case bridge.EventResponseDelta:
				response.WriteString(ev.Delta)
				all.WriteString(ev.Delta)
				out.beat(fmt.Sprintf("responding turn %d/%d", inferenceTurn, maxInferenceTurns))
				out.emit(runCtx, ev)
			case bridge.EventToolCall:
				out.emit(runCtx, ev)
				if ev.ToolCall != nil {
					out.beat("tool: " + ev.ToolCall.Name)
					toolCalls++
					if toolCalls > budget.MaxToolCalls {
						cancelAttempt()
						return all, fmt.Errorf("tool-call budget exhausted after %d calls", budget.MaxToolCalls)
					}
					signature := toolCallSignature(*ev.ToolCall)
					if signature == lastToolSignature {
						identicalToolCalls++
					} else {
						lastToolSignature = signature
						identicalToolCalls = 1
					}
					if identicalToolCalls > budget.MaxIdenticalToolCalls {
						cancelAttempt()
						return all, fmt.Errorf("loop detected: identical tool call repeated %d times", identicalToolCalls)
					}
					call := *ev.ToolCall
					pendingTools = append(pendingTools, scheduledTool{
						index: len(pendingTools),
						call:  call,
						execute: func(ctx context.Context) turnOutcome {
							return cfg.dispatch(withActorRunID(ctx, runID), call)
						},
					})
				}
			case bridge.EventError:
				if runCtx.Err() != nil {
					cancelAttempt()
					out.emit(context.Background(), bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
					if ctx.Err() != nil {
						return all, nil
					}
					return all, fmt.Errorf("continuation wall-time budget exhausted after %d minutes", budget.MaxWallTimeMinutes)
				}
				lastErr = ev.Error
				// A vision-rejection on an image-bearing request is recoverable:
				// mark the model text-only (memory + config) and retry this same
				// turn without the image so the run never gets stuck. Any other
				// error surfaces as before.
				if len(images) > 0 && !visionDowngraded && imageRejection(ev.Error) {
					o.setVisionSupport(snap.entry.Key, snap.entry.Model, false)
					o.persistVisionSupport(snap.entry.Key, snap.entry.Model, false)
					retryTextOnly = true
					out.emit(runCtx, statusEvent(sessionID, "model can't see images — retrying without the attachment"))
					cancelAttempt()
					break streamLoop
				}
				// A context/token-overflow 400 means our (guessed) context window
				// was too large. Force a compaction pass and retry instead of
				// failing — providers rarely expose the real limit, so the 400 is
				// the only reliable signal.
				if looksLikeContextOverflow(ev.Error) && forcedCompactions < maxForcedCompactions {
					retryCompacted = true
					out.emit(runCtx, statusEvent(sessionID, "context too large — compacting and retrying"))
					cancelAttempt()
					break streamLoop
				}
				// A transient transport hiccup (slow TTFB, reset, 5xx/429) is
				// worth retrying the same turn with a short backoff instead of
				// failing the whole task on one flaky request. Bounded by
				// maxTransportRetries; the wall-time budget is the final cap.
				if looksLikeTransientTransport(ev.Error) && transportRetries < maxTransportRetries {
					retryTransport = true
					out.emit(runCtx, statusEvent(sessionID, fmt.Sprintf("provider error — retrying (%d/%d)", transportRetries+1, maxTransportRetries)))
					cancelAttempt()
					break streamLoop
				}
				hadError = true
				out.emit(runCtx, ev)
				cancelAttempt()
				break streamLoop
			case bridge.EventThinkingDelta:
				// Accumulate reasoning so it can be persisted as a show-only
				// "thinking" turn (survives restart), then forward it live.
				if thinkingOut != nil {
					thinkingOut.WriteString(ev.Delta)
				}
				out.beat(fmt.Sprintf("thinking turn %d/%d", inferenceTurn, maxInferenceTurns))
				out.emit(runCtx, ev)
			case bridge.EventDone:
				// A bridge-level done ends one inference turn. The orchestrator
				// emits one final done after all tool continuations.
				cancelAttempt()
				break streamLoop
			default:
				out.emit(runCtx, ev)
			}
		}
		cancelAttempt()
		if len(pendingTools) > 0 && !retryTextOnly && !retryCompacted && !retryTransport && !hadError {
			results, batchStop := o.executeToolBatch(runCtx, runID, sessionID, pendingTools)
			toolResults = append(toolResults, results...)
			stop = stop || batchStop
		}
		if retryTextOnly {
			// Strip the images and re-run this inference turn text-only. The
			// per-attempt context above has already cancelled the broken stream;
			// never drain it synchronously because an uncooperative bridge may
			// never close its channel.
			visionDowngraded = true
			cleanMessages, _ = extractImages(cleanMessages)
			images = nil
			inferenceTurn--
			continue
		}
		if retryCompacted {
			// Force one compaction pass and re-run this same turn against the
			// shrunken history. The failed attempt is already cancelled. If
			// compaction can't shrink further (already minimal), recovery is
			// impossible — surface the original overflow error rather than loop.
			compacted := compactConversationMessages(cleanMessages, fallbackTask, runtimeCfg.Compaction.PreserveRecentFraction)
			if len(compacted) >= len(cleanMessages) {
				return all, fmt.Errorf("context overflow and conversation already minimal: %s", lastErr)
			}
			forcedCompactions++
			cleanMessages = compacted
			lastCompactedMessageCount = len(cleanMessages)
			cleanMessages, images = extractImages(cleanMessages)
			if len(images) > 0 && (visionDowngraded || !o.visionAllowed(snap.entry.Key, snap.entry.Model)) {
				images = nil
			}
			out.emit(runCtx, statusEvent(sessionID, "working"))
			inferenceTurn--
			continue
		}
		if retryTransport {
			// Wait a short exponential backoff (capped), then re-run this same
			// turn. The broken attempt is already cancelled, and accumulated
			// text from it is discarded by reusing cleanMessages unchanged.
			transportRetries++
			backoff := time.Duration(transportRetries) * transportRetryBaseBackoff
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
			select {
			case <-runCtx.Done():
				out.emit(context.Background(), bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
				if ctx.Err() != nil {
					return all, nil
				}
				return all, fmt.Errorf("continuation wall-time budget exhausted after %d minutes", budget.MaxWallTimeMinutes)
			case <-time.After(backoff):
			}
			inferenceTurn--
			continue
		}
		// A turn that finished without a transport error clears the retry
		// budget, so an occasional blip during a long run doesn't accumulate.
		transportRetries = 0
		if hadError {
			return all, nil
		}
		if len(images) > 0 {
			o.setVisionSupport(snap.entry.Key, snap.entry.Model, true)
			o.persistVisionSupport(snap.entry.Key, snap.entry.Model, true)
		}
		// An explicit stop (sapaloq_stop / terminal tool) always ends the run.
		// A tool-less turn ends it only for roles that finish naturally (chat,
		// planner); an executor must signal completion via a terminal tool, so
		// it keeps looping (bounded by the budgets + loop guards below) until it
		// does. Narrating without acting is allowed — the budgets, not a
		// bespoke narration guard, bound a misbehaving model.
		if stop || (cfg.finishOnNoTool && len(toolResults) == 0) {
			out.emit(runCtx, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			return all, nil
		}
		// NOTE: we intentionally do NOT fail a turn just because the model
		// narrated without calling a tool. Thinking/narrating before acting is
		// healthy model behavior (the same way any capable model reasons before
		// it acts) — penalising it cuts the model off before it gets to act.
		// A model that truly spins forever is still bounded by the real safety
		// nets: maxInferenceTurns (per-role turn cap), the wall-time budget,
		// MaxToolCalls, and the no-progress hash guard right below (which fires
		// only on genuinely identical, zero-progress turns).
		outcome := fmt.Sprintf("%x", sha256.Sum256([]byte(response.String()+"\x00"+strings.Join(toolResults, "\x00"))))
		if outcome == lastOutcome {
			noProgressTurns++
		} else {
			lastOutcome = outcome
			noProgressTurns = 0
		}
		if noProgressTurns >= budget.MaxNoProgressTurns {
			return all, fmt.Errorf("loop detected: no observable progress for %d inference turns", noProgressTurns)
		}
		// Build the continuation prompt. A tool-less turn for a non-finishing
		// role (executor narrating intent without acting) gets an explicit nudge
		// to either act or signal completion; a normal tool turn gets the
		// standard "continue using these results" follow-up.
		var toolResultsBody string
		if len(toolResults) == 0 {
			// Plain, non-coercive continuation for a turn that produced no tool
			// call. The model may simply be thinking before it acts — so we just
			// remind it of the available moves without threatening or rushing it.
			toolResultsBody = "You did not call any tool this turn and have not finished. " +
				"When you are ready, invoke the tool you need (e.g. exec, create_file, " +
				"edit_file), or call sapaloq_complete_task with a summary if the task is " +
				"done, or sapaloq_fail_task with a reason if it cannot be done."
		} else {
			toolResultsBody = "[Tool results]\n" + strings.Join(toolResults, "\n\n")
		}
		// Persist tool results as a "tool" turn so they count toward context
		// usage and auto-compaction. These messages ARE sent to the model (they
		// are appended to cleanMessages below and replayed via contextMessages,
		// which maps role "tool" → "assistant"), so leaving them unrecorded made
		// ContextUsage under-count and auto-compact trigger too late. Use the
		// outer ctx (not the cancelable runCtx) so a wall-time timeout does not
		// drop the audit/accounting record. Chat-only (recordToolTurns).
		if cfg.recordToolTurns && o.chat != nil && len(toolResults) > 0 {
			_ = o.chat.AppendTurn(ctx, sessionID, "tool", toolResultsBody, estimateTextTokens(toolResultsBody))
		}
		continuation := toolResultsBody
		if len(toolResults) > 0 {
			continuation += "\nContinue the original request using these results. Do not repeat the tool call unless another tool action is required."
		}
		// Inject a small, honest usage readout so the model has lightweight
		// self-awareness of how much work it has done so far. This is purely
		// informational — the budgets are set generously so they don't cage the
		// model; this just helps it pace itself. Cheap to render, ~1 line.
		continuation += fmt.Sprintf("\n\n[Usage] turn %d · tool-calls so far %d", inferenceTurn, toolCalls)
		// Record the tool calls this turn actually made into the assistant
		// message. response.String() carries only the model's text deltas — not
		// the tool_call itself — so without this the next turn sees the model's
		// narration ("I'll delegate to an agent…") followed by a tool result,
		// but no evidence that IT invoked the tool. Some models (e.g. Opus)
		// need that confirmation in-transcript to trust the action happened;
		// lacking it they second-guess ("I forgot to actually call it") and
		// re-issue the same call — the double-spawn bug. Appending an explicit
		// [Called tools: …] note gives that proof back. Models that don't need
		// it (e.g. minimax) are unaffected.
		assistantContent := response.String()
		if note := calledToolsNote(pendingTools); note != "" {
			if assistantContent != "" {
				assistantContent += "\n\n"
			}
			assistantContent += note
		}
		cleanMessages = append(cleanMessages,
			bridge.Message{Role: "assistant", Content: assistantContent},
			bridge.Message{Role: "user", Content: continuation},
		)
		// Re-extract images from the freshly appended tool-results message so a
		// read_image tool call (which returns inline-image markdown) becomes real
		// vision input on the next turn — the same channel widget attachments use.
		cleanMessages, images = extractImages(cleanMessages)
		// If the model is already known text-only (this run downgraded it, or a
		// prior run/config marked it), drop the images and keep going on text
		// instead of stalling — the markdown is already a text placeholder.
		if len(images) > 0 && (visionDowngraded || !o.visionAllowed(snap.entry.Key, snap.entry.Model)) {
			images = nil
		}
	}
	return all, fmt.Errorf("inference-turn budget exhausted after %d turns", maxInferenceTurns)
}

func toolCallSignature(call parse.ToolCall) string {
	return call.Name + "\x00" + strings.TrimSpace(string(call.Arguments))
}

// calledToolsNote renders an explicit, in-transcript record of the tools the
// assistant invoked on a turn, e.g. "[Called tools: sapaloq_spawn_agent]". It
// is appended to the assistant message so the model sees proof that it acted —
// the text delta stream alone does not include the tool_call. Duplicate names
// in the same turn are listed once with a ×N count to stay compact. Returns ""
// when no tools were called.
func calledToolsNote(tools []scheduledTool) string {
	if len(tools) == 0 {
		return ""
	}
	order := make([]string, 0, len(tools))
	counts := make(map[string]int, len(tools))
	for _, t := range tools {
		name := t.call.Name
		if _, seen := counts[name]; !seen {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if counts[name] > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", name, counts[name]))
		} else {
			parts = append(parts, name)
		}
	}
	return "[Called tools: " + strings.Join(parts, ", ") + "]"
}

func conversationTokenRatio(messages []bridge.Message, contextWindow int) float64 {
	if contextWindow <= 0 {
		contextWindow = defaultContextWindow
	}
	total := 0
	for _, message := range messages {
		total += estimateTextTokens(message.Content)
	}
	return float64(total) / float64(contextWindow)
}

func shouldCompactConversation(messages []bridge.Message, contextWindow int, threshold float64) bool {
	if threshold <= 0 {
		threshold = 0.70
	}
	return len(messages) > 8 && conversationTokenRatio(messages, contextWindow) >= threshold
}

func compactConversationMessages(messages []bridge.Message, originalTask string, preserveRecentFraction float64) []bridge.Message {
	if len(messages) <= 6 {
		return messages
	}
	if preserveRecentFraction <= 0 || preserveRecentFraction >= 1 {
		preserveRecentFraction = 0.30
	}
	system := messages[0]
	body := messages[1:]
	keep := int(math.Ceil(float64(len(body)) * preserveRecentFraction))
	if keep < 4 {
		keep = 4
	}
	if keep >= len(body) {
		return messages
	}
	cut := len(body) - keep
	var checkpoint strings.Builder
	checkpoint.WriteString("[Mid-run compacted checkpoint]\n")
	checkpoint.WriteString("Original task: ")
	checkpoint.WriteString(truncateForCheckpoint(originalTask, 600))
	checkpoint.WriteString("\nCompleted context:\n")
	for _, message := range body[:cut] {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		checkpoint.WriteString("- ")
		checkpoint.WriteString(message.Role)
		checkpoint.WriteString(": ")
		checkpoint.WriteString(truncateForCheckpoint(text, 360))
		checkpoint.WriteByte('\n')
	}
	checkpoint.WriteString("Resume the same task from the recent messages below. Do not restart completed work.")
	compacted := make([]bridge.Message, 0, keep+2)
	compacted = append(compacted, system)
	compacted = append(compacted, bridge.Message{Role: "system", Content: checkpoint.String()})
	compacted = append(compacted, body[cut:]...)
	return compacted
}

func truncateForCheckpoint(text string, limit int) string {
	text = inlineImageRE.ReplaceAllString(text, "[image attachment preserved separately]")
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

func extractImages(messages []bridge.Message) ([]bridge.Message, []bridge.Image) {
	cleaned := make([]bridge.Message, 0, len(messages))
	var images []bridge.Image
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUser = i
			break
		}
	}
	for index, message := range messages {
		content := attachmentMetaRE.ReplaceAllString(message.Content, "")
		content = inlineImageRE.ReplaceAllStringFunc(content, func(match string) string {
			parts := inlineImageRE.FindStringSubmatch(match)
			if len(parts) < 4 {
				return match
			}
			dataURI := parts[2]
			mime := parts[3]
			if validDataImage(dataURI) {
				if index != lastUser {
					return "[Prior image attachment: " + strings.TrimSpace(parts[1]) + "]"
				}
				images = append(images, bridge.Image{DataURI: dataURI, MimeType: mime})
				name := strings.TrimSpace(parts[1])
				if name == "" {
					name = "attached image"
				}
				return "[Image attachment: " + name + "]"
			}
			return match
		})
		cleaned = append(cleaned, bridge.Message{Role: message.Role, Content: content})
	}
	return cleaned, images
}

func validDataImage(dataURI string) bool {
	comma := strings.IndexByte(dataURI, ',')
	if comma < 0 {
		return false
	}
	if strings.Contains(dataURI[:comma], ";base64") {
		_, err := base64.StdEncoding.DecodeString(dataURI[comma+1:])
		return err == nil
	}
	return true
}

func (o *Orchestrator) visionAllowed(provider, model string) bool {
	o.visionMu.RLock()
	supported, known := o.vision[provider+"\x00"+model]
	o.visionMu.RUnlock()
	return !known || supported
}

func (o *Orchestrator) setVisionSupport(provider, model string, supported bool) {
	o.visionMu.Lock()
	o.vision[provider+"\x00"+model] = supported
	o.visionMu.Unlock()
}

// seedVisionFromConfig pre-populates the in-memory vision cache from any
// provider entry that has an explicit supportsImages value, so a model proven
// text-only in a previous session is honoured immediately on startup.
func (o *Orchestrator) seedVisionFromConfig(cfg config.Config) {
	for _, p := range cfg.LLMBridge.Providers {
		if p.SupportsImages == nil {
			continue
		}
		o.setVisionSupport(p.Key, p.Model, *p.SupportsImages)
	}
}

// persistVisionSupport records a discovered vision capability into config.json
// (provider entry matched by key, falling back to model) so a model proven
// text-only is never re-probed across restarts. Best-effort: a write failure
// only loses persistence, not the in-memory cache, and never aborts the run.
// Guarded by visionMu so concurrent runs don't race the config read/write.
func (o *Orchestrator) persistVisionSupport(providerKey, model string, supported bool) {
	if o.cfgPath == "" {
		return
	}
	o.visionMu.Lock()
	defer o.visionMu.Unlock()
	raw, err := config.LoadRaw(o.cfgPath)
	if err != nil {
		return
	}
	if !setProviderVisionFlag(raw, providerKey, model, supported) {
		return
	}
	if err := config.SaveRaw(o.cfgPath, raw, "orchestrator:vision-probe"); err != nil {
		return
	}
	if reloaded, rErr := config.ReloadFromRaw(o.cfgPath); rErr == nil {
		o.mu.Lock()
		o.cfg = reloaded
		o.mu.Unlock()
	}
}

// setProviderVisionFlag walks the raw config map to llmBridge.providers[] and
// sets supportsImages on the entry whose "key" matches providerKey (or, when
// that is empty/unmatched, whose "model" matches). Returns true when an entry
// was updated. Kept generic (map[string]any) so it survives unknown fields and
// preserves the rest of the file verbatim.
func setProviderVisionFlag(raw map[string]any, providerKey, model string, supported bool) bool {
	root, ok := raw["llmBridge"].(map[string]any)
	if !ok {
		return false
	}
	list, ok := root["providers"].([]any)
	if !ok {
		return false
	}
	matchByModel := -1
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if providerKey != "" {
			if k, _ := entry["key"].(string); k == providerKey {
				entry["supportsImages"] = supported
				return true
			}
		}
		if m, _ := entry["model"].(string); m == model && matchByModel < 0 {
			matchByModel = i
		}
	}
	if matchByModel >= 0 {
		if entry, ok := list[matchByModel].(map[string]any); ok {
			entry["supportsImages"] = supported
			return true
		}
	}
	return false
}

// imageRejection reports whether an upstream error on an image-bearing request
// most likely means the model can't accept images — so we should mark it
// text-only and retry without the attachment. Two signals:
//  1. An explicit vision-unsupported phrase (works regardless of status code,
//     and for providers that return the rejection inside a 200 body).
//  2. An HTTP 400 (the OpenAI-compatible class for a malformed/invalid request)
//     that is NOT one of the unrelated 4xx classes (auth, rate limit, quota,
//     billing, context-length). On an image request, a 400 that isn't one of
//     those is overwhelmingly the image itself.
//
// The caller only invokes this when images were actually attached, which is the
// primary false-positive guard.
func imageRejection(message string) bool {
	if looksLikeVisionUnsupported(message) {
		return true
	}
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "status 400") && !strings.Contains(lower, "400 ") {
		return false
	}
	return !mentionsUnrelated4xx(lower)
}

// looksLikeContextOverflow reports whether an upstream error means the request
// exceeded the model's context/token budget. The configured contextWindow is
// only a guess (providers rarely expose the real limit), so a too-large guess
// slips past our proactive compaction and surfaces here as a 400. When we see
// this we force a compaction pass and retry rather than failing the turn.
func looksLikeContextOverflow(message string) bool {
	lower := strings.ToLower(message)
	for _, kw := range []string{
		"context length", "context_length", "maximum context", "context window",
		"too many tokens", "tokens exceed", "exceed the", "reduce the length",
		"string too long", "maximum_tokens", "max_tokens",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// looksLikeTransientTransport reports whether an upstream error is a transient
// network/transport hiccup (a slow or flaky provider) that is worth retrying
// the same turn for, rather than a deterministic failure (auth, malformed
// request, context overflow) where retrying would just fail again. We retry on
// timeouts, dropped/reset connections, premature EOFs, and 5xx/429 responses —
// the classic "try again in a moment" class. Context-overflow is intentionally
// excluded here because it has its own dedicated compaction-and-retry path.
func looksLikeTransientTransport(message string) bool {
	lower := strings.ToLower(message)
	if looksLikeContextOverflow(lower) {
		return false
	}
	for _, kw := range []string{
		"timeout", "timed out", "deadline exceeded",
		"awaiting response headers", "awaiting headers",
		"connection reset", "connection refused", "broken pipe",
		"eof", "unexpected eof", "connection closed", "reset by peer",
		"no such host", "network is unreachable", "i/o timeout",
		"temporary failure", "tls handshake",
		"status 500", "status 502", "status 503", "status 504", "status 429",
		"500 ", "502 ", "503 ", "504 ", "429 ",
		"bad gateway", "service unavailable", "gateway timeout", "too many requests",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// mentionsUnrelated4xx detects 4xx causes that have nothing to do with vision,
// so a 400 carrying one of them is NOT treated as an image rejection.
func mentionsUnrelated4xx(lower string) bool {
	for _, kw := range []string{
		"rate limit", "ratelimit", "rate_limit", "429",
		"quota", "billing", "payment", "insufficient", "credit",
		"expired", "invalid api key", "invalid_api_key", "unauthorized",
		"authentication", "permission", "401", "403",
		"context length", "context_length", "maximum context", "too many tokens",
		"reduce the length", "context window",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func looksLikeVisionUnsupported(message string) bool {
	lower := strings.ToLower(message)
	// "text-only" / "multimodal" are strong vision signals on their own; they
	// almost never appear in unrelated errors. Phrasings around "image" need a
	// rejection verb to avoid matching incidental mentions.
	if strings.Contains(lower, "text-only") || strings.Contains(lower, "multimodal") {
		return true
	}
	return (strings.Contains(lower, "image") || strings.Contains(lower, "vision")) &&
		(strings.Contains(lower, "not support") ||
			strings.Contains(lower, "unsupported") ||
			strings.Contains(lower, "not allowed") ||
			strings.Contains(lower, "cannot process") ||
			strings.Contains(lower, "can't process"))
}
