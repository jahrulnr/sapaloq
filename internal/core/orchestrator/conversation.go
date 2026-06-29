package orchestrator

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/parse"
	"github.com/jahrulnr/sapaloq/internal/parse/artifacts"
)

// errStreamErrorSurfaced wraps a non-recoverable provider/stream error that the
// turn loop has ALREADY emitted to its sink (as an EventError) before
// returning. runTurnLoop returns it so the sub-agent finalizer can mark the
// task `failed` (it keys off a non-nil error), while the chat caller uses
// errors.Is to AVOID emitting a second, duplicate EventError to the widget.
var errStreamErrorSurfaced = errors.New("stream error already surfaced to sink")

var inlineImageRE = regexp.MustCompile(`!\[([^\]]*)\]\((data:(image/[^;,]+)(?:;base64)?,[^)]+)\)`)
var attachmentMetaRE = regexp.MustCompile(`<!--sapaloq-attachment:[A-Za-z0-9+/=]+-->`)

// transportRetryBaseBackoff is the per-attempt backoff unit for retrying a turn
// after a transient transport error (attempt N waits N×base, capped at 5s). It
// is a package var only so tests can zero it to run instantly.
var transportRetryBaseBackoff = 750 * time.Millisecond

// idleWindowUnit is the multiplier applied to MaxWallTimeMinutes to form the
// inactivity (idle) deadline. It is the real minute in production and a package
// var only so tests can shrink it (e.g. to a few ms) to exercise the idle
// timeout deterministically without waiting whole minutes.
var idleWindowUnit = time.Minute

// runConversation drives one Ask (chat) turn. If thinkingOut is non-nil,
// reasoning (EventThinkingDelta) text is accumulated into it so the caller can
// persist it as a show-only "thinking" turn - separate from the assistant
// answer (`all`). It is a thin wrapper that configures the SHARED engine
// (runTurnLoop) for the chat role: the full Ask tool surface, a live channel
// sink, no heartbeat, and natural finish on a tool-less turn.
func (o *Orchestrator) runConversation(ctx context.Context, snap providerSnapshot, out chan<- bridge.StreamEvent, sessionID, fallbackTask string, messages []bridge.Message, thinkingOut *strings.Builder) (strings.Builder, error) {
	return o.runConversationWithGeneration(ctx, snap, out, sessionID, "", fallbackTask, messages, thinkingOut)
}

func (o *Orchestrator) runConversationWithGeneration(ctx context.Context, snap providerSnapshot, out chan<- bridge.StreamEvent, sessionID, generationID, fallbackTask string, messages []bridge.Message, thinkingOut *strings.Builder) (strings.Builder, error) {
	return o.runConversationActor(ctx, snap, out, sessionID, sessionID, generationID, fallbackTask, messages, thinkingOut)
}

// runConversationActor runs the shared Ask engine under an explicit actor id.
// Foreground chat uses sessionID; invisible mediators use their own runID while
// sharing the same bounded conversation snapshot, preventing mailbox/tool-job
// identity conflicts with a concurrently active UI orchestrator.
func (o *Orchestrator) runConversationActor(ctx context.Context, snap providerSnapshot, out chan<- bridge.StreamEvent, sessionID, runID, generationID, fallbackTask string, messages []bridge.Message, thinkingOut *strings.Builder) (strings.Builder, error) {
	return o.runActor(ctx, snap, ActorRun{
		ID: runID, ParentSessionID: sessionID, Role: "ask",
		GenerationID: generationID, TaskText: fallbackTask, Tools: askTools,
		Messages: messages, Foreground: true, Out: out, ThinkingOut: thinkingOut,
	})
}

// runTurnLoop is the single multi-turn inference engine behind both chat and
// every sub-agent role. The variable parts (tool surface, per-call dispatch,
// output sink, finish policy, heartbeat) are supplied via turnConfig; the
// resilience logic (wall-time/turn/tool budgets, identical-tool and
// toolless-turn loop bounds, proactive + overflow-triggered compaction, vision
// downgrade, clean stream/error handling) is shared so a fix lands once for
// everyone.
func (o *Orchestrator) runTurnLoop(ctx context.Context, snap providerSnapshot, fallbackTask string, messages []bridge.Message, cfg turnConfig) (strings.Builder, error) {
	sessionID := cfg.sessionID
	persistID := cfg.persistID
	if persistID == "" {
		persistID = sessionID
	}
	runID := cfg.runID
	if runID == "" {
		runID = sessionID
	}
	out := cfg.sink
	thinkingOut := cfg.thinkingOut
	var all strings.Builder
	cleanMessages, images := extractImages(messages)
	if cfg.compactCtx != nil {
		cfg.compactCtx.messages = &cleanMessages
		cfg.compactCtx.fallbackTask = fallbackTask
	}
	// One-shot: once we've dropped images and retried text-only we never
	// re-attach them in this run, so a model that can't see images degrades
	// gracefully instead of looping on the same 400.
	visionDowngraded := false
	// If this model is already known text-only (marked false in a prior run or
	// in config), don't bother sending the image - drop it and proceed on the
	// text placeholder so the user still gets an answer instead of an error.
	if len(images) > 0 && !o.visionAllowed(snap.entry.Key, snap.entry.Model) {
		images = nil
		visionDowngraded = true
	}
	runtimeCfg := snap.cfg.Orchestrator.WithDefaults()
	budget := runtimeCfg.Continuation
	// Wall-time is an INACTIVITY (idle) deadline, not a total-runtime cap. A
	// productive agent that keeps streaming output / making tool calls must be
	// allowed to work as long as it is making progress - capping total runtime
	// would kill a healthy long task (e.g. scaffolding a large app) mid-work,
	// which is exactly the wrong thing to punish. Instead we cancel only when
	// the run goes SILENT for MaxWallTimeMinutes (a stuck network / dead stream
	// / wedged turn). The timer is reset on every observable progress event
	// (turn start + each delta/tool-call), mirroring the heartbeat the stall
	// watchdog uses. Per-request network hangs remain covered by the provider
	// bridge's own RequestTimeout.
	idleWindow := time.Duration(budget.MaxWallTimeMinutes) * idleWindowUnit
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	idleTimer := time.AfterFunc(idleWindow, cancel)
	defer idleTimer.Stop()
	resetIdle := func() { idleTimer.Reset(idleWindow) }
	// toollessBudget bounds "loop turns without a tool" under the explicit-stop
	// model: a turn that calls NO tool is not a completion signal (no "no-tool =
	// stop"), so the run would otherwise keep looping while a model only
	// narrates. Each tool-less turn burns 1 from the budget; each turn that
	// calls a tool refills 1; once the model has proven productive (>10 total
	// tool calls) every turn is accepted and the budget is topped up so a long
	// working session is never cut off mid-flow. When the budget hits 0 the run
	// ends cleanly. Seeded from Continuation.MaxNoProgressTurns (default 10);
	// a value <= 0 disables the bound entirely (observe raw model behavior).
	toollessBudget := budget.MaxNoProgressTurns
	lastToolSignature := ""
	identicalToolCalls := 0
	toolCalls := 0
	toollessStreak := 0
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

	// Foreground ask: retry empty/noise api2 turns before showing FallbackAskNoiseRetry.
	noiseRetries := 0
	const maxNoiseRetries = 3

	// Bounds how many consecutive turns may emit tool calls that produce no
	// usable result (a non-native model leaking malformed inline tool calls).
	// Instead of mistaking such a turn for a clean tool-less finish and ending
	// the run prematurely, we nudge the model to retry one call at a time; this
	// caps that nudge so a model that can never produce a valid call still
	// finishes instead of looping forever.
	malformedToolTurns := 0
	const maxMalformedToolTurns = 3
	var turnThinking strings.Builder

	// maxInferenceTurns: a positive value caps the loop; a NEGATIVE value means
	// UNLIMITED - the loop is then bounded only by the real anomaly guards
	// (wall-time, toolless-turn budget, identical-tool, tool-call budgets). cfg
	// overrides the config default when set to any non-zero value (so an
	// explicit -1 from a role survives instead of falling back to the bounded
	// config default).
	maxInferenceTurns := budget.MaxInferenceTurns
	if cfg.maxInferenceTurns != 0 {
		maxInferenceTurns = cfg.maxInferenceTurns
	}
	unlimitedTurns := maxInferenceTurns < 0
	turnBudgetLabel := fmt.Sprintf("%d", maxInferenceTurns)
	if unlimitedTurns {
		turnBudgetLabel = "∞"
	}
	steeringPendingAck := false
	emitSteeringSkipped := func() {
		if steeringPendingAck {
			out.emit(runCtx, statusEvent(sessionID, "steering skipped - run ended"))
			steeringPendingAck = false
			return
		}
		if o.skipPendingSteering(runID) {
			out.emit(runCtx, statusEvent(sessionID, "steering skipped - run ended"))
		}
	}

	for inferenceTurn := 1; unlimitedTurns || inferenceTurn <= maxInferenceTurns; inferenceTurn++ {
		turnThinking.Reset()
		allTurnStart := all.Len()
		if err := runCtx.Err(); err != nil {
			out.emit(context.Background(), bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			if ctx.Err() != nil {
				return all, nil
			}
			return all, fmt.Errorf("run stalled: no activity for %d minutes", budget.MaxWallTimeMinutes)
		}
		if messages, applied := o.appendActorEvents(cleanMessages, runID); applied {
			cleanMessages = messages
			steeringPendingAck = true
		}
		if steeringPendingAck {
			out.emit(runCtx, statusEvent(sessionID, "steering applied"))
			steeringPendingAck = false
		}
		// Heartbeat at the top of every turn so the health watchdog can tell a
		// genuinely-working agent (advancing turns) from a wedged goroutine.
		// Advancing a turn is progress - reset the inactivity deadline.
		resetIdle()
		out.beat(fmt.Sprintf("inference turn %d/%s", inferenceTurn, turnBudgetLabel))
		if runtimeCfg.Compaction.UseCheckpointsEnabled() && !cfg.suppressHeadroomCompaction {
			// Pre-turn soft compact at ~90% (closes dead zone before 95% headroom).
			preTurnPct := 90
			if steer := int(runtimeCfg.Compaction.SteerPercent); steer > 0 && steer < 95 {
				preTurnPct = steer + 5
				if preTurnPct > 94 {
					preTurnPct = 94
				}
			}
			headroomThreshold := int((1.0 - runtimeCfg.Compaction.HeadroomPercent) * 100)
			if headroomThreshold <= 0 {
				headroomThreshold = 95
			}
			pct := o.effectiveContextPercent(runCtx, sessionID, cleanMessages, snap.entry.ContextWindow)
			if pct >= preTurnPct && pct < headroomThreshold &&
				len(cleanMessages) > lastCompactedMessageCount+2 {
				out.emit(runCtx, statusEvent(sessionID, "compacting"))
				if cfg.compactCtx != nil {
					before := len(cleanMessages)
					if err := o.runSubAgentCompact(runCtx, snap, cfg.compactCtx, "pre_turn"); err == nil && len(*cfg.compactCtx.messages) < before {
						cleanMessages = *cfg.compactCtx.messages
						lastCompactedMessageCount = len(cleanMessages)
						out.emit(runCtx, statusEvent(sessionID, "working"))
					}
				} else {
					shrunk, ok, cerr := o.shrinkContextForRun(runCtx, snap, out, sessionID, fallbackTask, "pre_turn", cleanMessages, true)
					if cerr == nil && ok {
						cleanMessages = shrunk
						lastCompactedMessageCount = len(cleanMessages)
						out.emit(runCtx, statusEvent(sessionID, "working"))
					}
				}
			}
			// Isolated checkpoint compaction at the headroom threshold
			// (default 5% remaining). Require a few new messages since the last
			// pass so a DB-heavy pill cannot re-trigger blocking compaction every
			// turn when only the in-memory slice was heuristically shrunk.
			if o.contextHeadroomReached(runCtx, sessionID, cleanMessages, snap.entry.ContextWindow, runtimeCfg.Compaction.HeadroomPercent) &&
				len(cleanMessages) > lastCompactedMessageCount+2 {
				out.emit(runCtx, statusEvent(sessionID, "compacting"))
				if cfg.compactCtx != nil {
					before := len(cleanMessages)
					if err := o.runSubAgentCompact(runCtx, snap, cfg.compactCtx, "force_headroom"); err != nil {
						return all, fmt.Errorf("context at %d%% and compaction could not shrink history: %v", o.contextPercent(cleanMessages, snap.entry.ContextWindow), err)
					}
					if len(*cfg.compactCtx.messages) >= before {
						return all, fmt.Errorf("context at %d%% and compaction could not shrink history", o.contextPercent(cleanMessages, snap.entry.ContextWindow))
					}
					cleanMessages = *cfg.compactCtx.messages
					lastCompactedMessageCount = len(cleanMessages)
					out.emit(runCtx, statusEvent(sessionID, "working"))
				} else {
					shrunk, ok, cerr := o.shrinkContextForRun(runCtx, snap, out, sessionID, fallbackTask, "force_headroom", cleanMessages, true)
					if cerr != nil {
						return all, cerr
					}
					if !ok {
						return all, fmt.Errorf("context at %d%% and compaction could not shrink history; run /compaction or /reset", o.effectiveContextPercent(runCtx, sessionID, cleanMessages, snap.entry.ContextWindow))
					}
					cleanMessages = shrunk
					lastCompactedMessageCount = len(cleanMessages)
					out.emit(runCtx, statusEvent(sessionID, "working"))
				}
			}
		} else if !runtimeCfg.Compaction.UseCheckpointsEnabled() {
			pct := o.effectiveContextPercent(runCtx, sessionID, cleanMessages, snap.entry.ContextWindow)
			bgThreshold := int(runtimeCfg.Compaction.BackgroundThreshold * 100)
			if bgThreshold <= 0 {
				bgThreshold = 70
			}
			blockThreshold := int(runtimeCfg.Compaction.BlockingThreshold * 100)
			if blockThreshold <= 0 {
				blockThreshold = 88
			}
			if pct >= bgThreshold && len(cleanMessages) > lastCompactedMessageCount+2 {
				blocking := pct >= blockThreshold
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
		}

		out.emit(runCtx, statusEvent(sessionID, "working"))
		// Anthropic/Vercel gateways reject requests whose last non-system turn is
		// assistant (assistant prefill). After checkpoint tail-anchoring the live
		// slice often ends on assistant — inject a synthetic user continuation.
		attemptMessages := ensureConversationEndsWithUser(cleanMessages)
		attemptCtx, cancelAttempt := context.WithCancel(runCtx)
		var dynamicStop atomic.Bool
		var dynamicProgress atomic.Bool
		stream, err := snap.br.Complete(attemptCtx, bridge.Request{
			SessionID:            sessionID,
			ConversationScope:    cfg.generationID,
			ProviderContinuation: inferenceTurn > 1,
			Messages:             attemptMessages,
			Model:                snap.entry.Model,
			DeclaredTools:        cfg.tools,
			Images:               images,
			ToolExecutor: func(callCtx context.Context, call parse.ToolCall) (string, error) {
				outcome := cfg.dispatch(withActorRunID(callCtx, runID), call)
				if !outcome.handled {
					return "", fmt.Errorf("tool %q was not handled", call.Name)
				}
				dynamicProgress.Store(true)
				if outcome.stop {
					dynamicStop.Store(true)
				}
				return outcome.text, nil
			},
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
		// ctFilter strips any "[Called tools: …]" note the model echoes back
		// into its visible text (it learns the shape from the in-transcript
		// record calledToolsNote injects). The echo is not a real tool call and
		// must not reach the user or the persisted assistant message.
		var ctFilter calledToolsFilter
		var toolResults []string
		var pendingTools []scheduledTool
		stop := false
		hadError := false
		retryTextOnly := false
		retryCompacted := false
		retryTransport := false
		lastErr := ""
		// Count tool calls the model emitted this turn (including inline ones a
		// non-native model leaks into its content). Used below to tell a turn
		// that genuinely produced no tool from one where the model TRIED to
		// call tools but they failed to parse/execute (the latter must not be
		// mistaken for a clean tool-less finish).
		toolCallsThisTurn := 0
		steeringInterrupted := false
	streamLoop:
		for {
			var ev bridge.StreamEvent
			select {
			case <-runCtx.Done():
				cancelAttempt()
				break streamLoop
			case <-o.actorSignal(runID):
				if messages, applied := o.appendActorEvents(cleanMessages, runID); applied {
					cleanMessages = messages
					steeringInterrupted = true
					steeringPendingAck = true
				}
				cancelAttempt()
				break streamLoop
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
				resetIdle()
				out.beat(fmt.Sprintf("responding turn %d/%s", inferenceTurn, turnBudgetLabel))
				// Drop any echoed "[Called tools: …]" note before it is
				// streamed or persisted. The filter may withhold a trailing
				// fragment (the marker can split across deltas), so an empty
				// result here just means "still deciding" - flushed on done.
				clean := ctFilter.feed(ev.Delta)
				if clean == "" {
					continue
				}
				response.WriteString(clean)
				all.WriteString(clean)
				ev.Delta = clean
				out.emit(runCtx, ev)
			case bridge.EventToolCall:
				resetIdle()
				if ev.ToolCall != nil {
					// sapaloq:boundary cursor-bridge→orchestrator — in-bridge MCP: execute in bridge; persist on ToolUpdate.
					if isInBridgeToolSource(ev.ToolCall.Source) {
						debug.TraceBoundary("cursor-bridge", "orchestrator", "in_bridge_tool:"+ev.ToolCall.Name)
						toolCalls++
						toolCallsThisTurn++
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
						if budget.MaxIdenticalToolCalls > 0 && identicalToolCalls > budget.MaxIdenticalToolCalls {
							cancelAttempt()
							return all, fmt.Errorf("loop detected: identical tool call repeated %d times", identicalToolCalls)
						}
						out.emit(runCtx, ev)
						out.beat(ev.ToolCall.Source + " tool: " + ev.ToolCall.Name)
						continue
					}
					// Some inline/non-native providers do not assign tool-call IDs.
					// Give every call a stable per-run identity so its later result can
					// update the correct expandable UI block.
					if ev.ToolCall.ID == "" {
						ev.ToolCall.ID = fmt.Sprintf("%s:%d:%d", runID, inferenceTurn, len(pendingTools))
					}
					*ev.ToolCall = normalizeUpstreamToolCall(*ev.ToolCall)
					out.emit(runCtx, ev)
					out.beat("tool: " + ev.ToolCall.Name)
					toolCalls++
					toolCallsThisTurn++
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
					// budget.MaxIdenticalToolCalls <= 0 disables this guard.
					if budget.MaxIdenticalToolCalls > 0 && identicalToolCalls > budget.MaxIdenticalToolCalls {
						cancelAttempt()
						return all, fmt.Errorf("loop detected: identical tool call repeated %d times", identicalToolCalls)
					}
					call := *ev.ToolCall
					item := scheduledTool{
						index: len(pendingTools),
						call:  call,
						execute: func(ctx context.Context) turnOutcome {
							return cfg.dispatch(withActorRunID(ctx, runID), call)
						},
					}
					// Early read-only execution: when the experimental flag is
					// on and the tool has no side effects, dispatch it now in
					// a goroutine so its result is computed in parallel with
					// the rest of the stream, instead of all tools waiting for
					// the turn's EventDone before any of them start. The
					// result is read back by collectToolJobs via `future`.
					if budget.EarlyToolExecution && isReadOnlyAssessmentTool(call.Name) {
						fut := make(chan turnOutcome, 1)
						item.future = fut
						earlyCtx, earlyCancel := context.WithCancel(runCtx)
						go func() {
							outcome := cfg.dispatch(withActorRunID(earlyCtx, runID), call)
							select {
							case fut <- outcome:
							default:
							}
							earlyCancel()
						}()
					}
					pendingTools = append(pendingTools, item)
				} else {
					out.emit(runCtx, ev)
				}
			case bridge.EventError:
				if runCtx.Err() != nil {
					cancelAttempt()
					out.emit(context.Background(), bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
					if ctx.Err() != nil {
						return all, nil
					}
					return all, fmt.Errorf("run stalled: no activity for %d minutes", budget.MaxWallTimeMinutes)
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
					out.emit(runCtx, statusEvent(sessionID, "model can't see images - retrying without the attachment"))
					cancelAttempt()
					break streamLoop
				}
				// A context/token-overflow 400 means our (guessed) context window
				// was too large. Force a compaction pass and retry instead of
				// failing - providers rarely expose the real limit, so the 400 is
				// the only reliable signal.
				if looksLikeContextOverflow(ev.Error) && forcedCompactions < maxForcedCompactions {
					retryCompacted = true
					out.emit(runCtx, statusEvent(sessionID, "context too large - compacting and retrying"))
					cancelAttempt()
					break streamLoop
				}
				// A transient transport hiccup (slow TTFB, reset, 5xx/429) is
				// worth retrying the same turn with a short backoff instead of
				// failing the whole task on one flaky request. Bounded by
				// maxTransportRetries; the wall-time budget is the final cap.
				if looksLikeTransientTransport(ev.Error) && transportRetries < maxTransportRetries {
					retryTransport = true
					out.emit(runCtx, statusEvent(sessionID, fmt.Sprintf("provider error - retrying (%d/%d)", transportRetries+1, maxTransportRetries)))
					cancelAttempt()
					break streamLoop
				}
				hadError = true
				out.emit(runCtx, ev)
				cancelAttempt()
				break streamLoop
			case bridge.EventThinkingDelta:
				// Reasoning tokens are progress too (the model is working, not
				// stuck) - reset the inactivity deadline. Accumulate reasoning so
				// it can be persisted as a show-only "thinking" turn (survives
				// restart), then forward it live.
				resetIdle()
				turnThinking.WriteString(ev.Delta)
				if thinkingOut != nil {
					thinkingOut.WriteString(ev.Delta)
				}
				out.beat(fmt.Sprintf("thinking turn %d/%s", inferenceTurn, turnBudgetLabel))
				out.emit(runCtx, ev)
			case bridge.EventToolUpdate:
				resetIdle()
				if ev.ToolCall != nil && isInBridgeToolSource(ev.ToolCall.Source) {
					dynamicProgress.Store(true)
					o.persistInBridgeToolUpdate(ctx, persistID, cfg.generationID, cfg, &response, &turnThinking, &cleanMessages, ev)
				}
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
		if runCtx.Err() != nil {
			emitSteeringSkipped()
			if cfg.recordToolTurns {
				body := strings.TrimSpace(artifacts.StripModelResponseArtifact(StripCalledToolsMarkers(response.String())))
				if body != "" && !artifacts.IsAutopilotEcho(body) {
					o.persistAssistantTurnWithThinking(ctx, persistID, body, cfg.generationID, cfg, &turnThinking)
				} else {
					o.flushTurnThinking(ctx, persistID, cfg.generationID, cfg, &turnThinking)
				}
			}
			out.emit(context.Background(), bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			if ctx.Err() != nil {
				return all, nil
			}
			return all, fmt.Errorf("run stalled: no activity for %d minutes", budget.MaxWallTimeMinutes)
		}
		// The stream for this attempt has ended (done, channel close, or
		// cancellation). Release any text the calledToolsFilter was withholding
		// as a possible "[Called tools: …]" marker that turned out to be
		// ordinary text; an unterminated marker body is intentionally dropped.
		if tail := ctFilter.flush(); tail != "" {
			response.WriteString(tail)
			all.WriteString(tail)
			out.emit(runCtx, bridge.StreamEvent{Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: tail, At: time.Now().UTC()})
		}
		if messages, applied := o.appendActorEvents(cleanMessages, runID); applied {
			cleanMessages = messages
			steeringPendingAck = true
		}
		if len(pendingTools) > 0 && !retryTextOnly && !retryCompacted && !retryTransport && !hadError {
			beforeCheckpoint, _ := o.latestCheckpointIndex(runCtx, sessionID)
			results, batchStop := o.executeToolBatch(runCtx, runID, sessionID, pendingTools)
			for _, result := range results {
				if !result.handled {
					update := bridge.NewEvent(bridge.EventToolUpdate)
					update.SessionID = sessionID
					call := result.call
					update.ToolCall = &call
					update.ToolResult = "Tool call was not handled."
					update.Status = "failed"
					out.emit(runCtx, update)
					continue
				}
				redacted := o.redactToolResults([]string{result.text})
				if len(redacted) == 0 {
					continue
				}
				toolResults = append(toolResults, redacted[0])
				update := bridge.NewEvent(bridge.EventToolUpdate)
				update.SessionID = sessionID
				call := result.call
				update.ToolCall = &call
				update.ToolResult = truncateToolResultForUI(redacted[0])
				update.Status = "completed"
				out.emit(runCtx, update)
			}
			stop = stop || batchStop
			if afterCheckpoint, _ := o.latestCheckpointIndex(runCtx, sessionID); afterCheckpoint > beforeCheckpoint && cfg.recordToolTurns {
				if rebuilt, rerr := o.rebuildAfterCheckpoint(runCtx, sessionID, cleanMessages); rerr == nil {
					cleanMessages = rebuilt
					lastCompactedMessageCount = len(cleanMessages)
				}
			}
			if messages, applied := o.appendActorEvents(cleanMessages, runID); applied {
				cleanMessages = messages
				steeringPendingAck = true
			}
		}
		stop = stop || dynamicStop.Load()
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
			// Provider 400: run isolated compaction (tool-free summarization for
			// chat sessions; in-memory path for sub-agents) before retrying.
			out.emit(runCtx, statusEvent(sessionID, "compacting"))
			if cfg.compactCtx != nil {
				if err := o.runSubAgentCompact(runCtx, snap, cfg.compactCtx, "force_overflow"); err != nil {
					return all, fmt.Errorf("context overflow and conversation already minimal: %s", lastErr)
				}
				cleanMessages = *cfg.compactCtx.messages
				lastCompactedMessageCount = len(cleanMessages)
			} else {
				shrunk, ok, cerr := o.shrinkContextForRun(runCtx, snap, out, sessionID, fallbackTask, "force_overflow", cleanMessages, true)
				if cerr != nil {
					return all, cerr
				}
				if !ok {
					return all, fmt.Errorf("context overflow and conversation already minimal: %s", lastErr)
				}
				cleanMessages = shrunk
				lastCompactedMessageCount = len(cleanMessages)
			}
			forcedCompactions++
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
				return all, fmt.Errorf("run stalled: no activity for %d minutes", budget.MaxWallTimeMinutes)
			case <-time.After(backoff):
			}
			inferenceTurn--
			continue
		}
		// A turn that finished without a transport error clears the retry
		// budget, so an occasional blip during a long run doesn't accumulate.
		transportRetries = 0
		if steeringInterrupted {
			if body := strings.TrimSpace(response.String()); body != "" {
				cleanMessages = append(cleanMessages, bridge.Message{Role: "assistant", Content: body})
			}
			response.Reset()
			turnThinking.Reset()
			if thinkingOut != nil {
				thinkingOut.Reset()
			}
			continue
		}
		if hadError {
			emitSteeringSkipped()
			// The error event itself was already emitted to the sink above;
			// the dedicated EventDone is what unblocks the chat IPC consumer
			// (and the widget) - without it, the channel closes silently and
			// the frontend can stay in its "submitting" state because no
			// terminal event was seen. We use the turn's context (already
			// cancelled by the broken attempt) just to match the rest of the
			// call sites; the emit is fire-and-forget and is safe even if
			// the context is done.
			out.emit(context.Background(), bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			// Propagate the error to the CALLER. The UI already saw the error
			// (emitted to the sink) - but sub-agent finalization in
			// runTaskActor keys "failed vs done" off this returned error.
			// Returning nil here used to make a planner whose only LLM call hit
			// a provider 500 finish as "done" with a fake "Selesai." and no
			// plan.md, so Ask then narrated a plan that never existed. A
			// non-recoverable provider error is a real failure: surface it.
			// Wrap with errStreamErrorSurfaced so the chat caller knows the
			// EventError was already emitted and must not duplicate it.
			detail := lastErr
			if strings.TrimSpace(detail) == "" {
				detail = "inference failed"
			}
			return all, fmt.Errorf("%s: %w", detail, errStreamErrorSurfaced)
		}
		if len(images) > 0 {
			o.setVisionSupport(snap.entry.Key, snap.entry.Model, true)
			o.persistVisionSupport(snap.entry.Key, snap.entry.Model, true)
		}
		// Malformed-tool-call recovery (non-native function calling). A model
		// that emits tool calls inline in its content (source "openai_inline",
		// e.g. MiniMax-M3) can produce a turn where tool calls were EMITTED
		// (toolCallsThisTurn > 0) but none parsed/executed into a result
		// (toolResults empty) - typically a multi-call batch whose JSON got
		// mangled by leaked template tokens. Without this guard that turn looks
		// identical to a clean tool-less finish and the run would end mid-task
		// with no answer. Instead, nudge the model to retry one call at a time
		// and continue. Bounded by maxMalformedToolTurns so a model that can
		// never emit a valid call still finishes rather than looping forever.
		if !stop && toolCallsThisTurn > 0 && len(toolResults) == 0 && malformedToolTurns < maxMalformedToolTurns {
			malformedToolTurns++
			nudge := "Your previous tool call(s) could not be parsed or executed - likely " +
				"because they were emitted as inline text or batched together with stray " +
				"markers. Re-issue with the proper structured tool-call format: one " +
				"well-formed call, or separate calls for unrelated files (those can run " +
				"in parallel). If no tool is needed, answer in plain text."
			cleanMessages = append(cleanMessages,
				bridge.Message{Role: "assistant", Content: response.String()},
				bridge.Message{Role: "user", Content: nudge},
			)
			out.emit(runCtx, statusEvent(sessionID, "retrying malformed tool call"))
			continue
		}
		// A turn that produced a usable tool result clears the malformed guard
		// so an occasional bad batch during a long run doesn't accumulate.
		if len(toolResults) > 0 {
			malformedToolTurns = 0
		}
		// Drop confabulated edit artifacts (### Final file content, large unrelated
		// source dumps) that Cursor/api2 sometimes emits on innocent chat turns.
		if body := strings.TrimSpace(response.String()); body != "" && artifacts.IsModelResponseArtifact(body) {
			response.Reset()
			if all.Len() > allTurnStart {
				buf := all.String()
				all.Reset()
				all.WriteString(buf[:allTurnStart])
			}
			if cfg.foregroundAsk {
				msg := artifacts.FallbackAskNoiseRetry()
				response.WriteString(msg)
				all.WriteString(msg)
				out.emit(runCtx, bridge.StreamEvent{
					Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: msg, At: time.Now().UTC(),
				})
				turnThinking.Reset()
			}
			stop = true
		}
		if thinkingOut != nil && (artifacts.IsThinkingConfabulation(thinkingOut.String()) ||
			artifacts.IsUnanchoredThinkingConfabulation(thinkingOut.String(), cfg.taskAnchor)) {
			thinkingOut.Reset()
			turnThinking.Reset()
		}
		// Foreground ask chat: same loop as agent/planner — visible tool-less
		// text is never a stop signal (only sapaloq_stop or structural budgets).
		// When Cursor returns thinking-only with no visible text, still apply
		// ping greeting / noise retry so innocent turns are not blank.
		if !stop && cfg.foregroundAsk && toolCallsThisTurn == 0 && len(toolResults) == 0 {
			sig := o.sessionSignals(sessionID)
			if sig.runningTasks == 0 && !sig.awaitingClarification {
				visible := strings.TrimSpace(StripCalledToolsMarkers(response.String()))
				if visible == "" && toolCalls == 0 {
					if artifacts.IsConversationalPing(fallbackTask) {
						msg := artifacts.FallbackAskGreeting()
						response.WriteString(msg)
						all.WriteString(msg)
						out.emit(runCtx, bridge.StreamEvent{
							Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: msg, At: time.Now().UTC(),
						})
						turnThinking.Reset()
						stop = true
					} else if noiseRetries < maxNoiseRetries {
						noiseRetries++
						out.emit(runCtx, statusEvent(sessionID, fmt.Sprintf("provider noise - retrying (%d/%d)", noiseRetries, maxNoiseRetries)))
						continue
					} else {
						msg := artifacts.FallbackAskNoiseRetry()
						response.WriteString(msg)
						all.WriteString(msg)
						out.emit(runCtx, bridge.StreamEvent{
							Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: msg, At: time.Now().UTC(),
						})
						turnThinking.Reset()
						stop = true
					}
				}
			}
		}
		// Duplicate guard: thinking reset after policy so flushTurnThinking never persists bleed.
		if turnThinking.Len() > 0 && (artifacts.IsThinkingConfabulation(turnThinking.String()) ||
			artifacts.IsUnanchoredThinkingConfabulation(turnThinking.String(), cfg.taskAnchor)) {
			turnThinking.Reset()
		}
		// The ONLY explicit end signal is a terminal tool (chat: sapaloq_stop;
		// sub-agent: sapaloq_stop / sapaloq_complete_task / sapaloq_fail_task),
		// surfaced here as `stop`. A tool-less turn NEVER ends the run - the
		// absence of a tool call is not a completion signal, it is just a turn
		// that narrated, reasoned, or answered without acting. We do NOT
		// inspect the model's text to guess "done vs still working" (the old
		// NO_OP/intent tebak-tebakan), and we do NOT use a second model to
		// judge it. The run simply continues; the structural budgets below
		// (turn cap, idle wall-time, MaxToolCalls, toolless-turn budget) are
		// the only bounds.
		if stop {
			emitSteeringSkipped()
			if cfg.recordToolTurns {
				final := response.String()
				if final != "" {
					if note := calledToolsNote(pendingTools); note != "" {
						final += "\n\n" + note
					}
					o.persistAssistantTurnWithThinking(ctx, persistID, final, cfg.generationID, cfg, &turnThinking)
				} else if note := calledToolsNote(pendingTools); note != "" {
					o.flushTurnThinking(ctx, persistID, cfg.generationID, cfg, &turnThinking)
					o.persistAssistantTurn(ctx, persistID, note, cfg.generationID)
				} else {
					o.flushTurnThinking(ctx, persistID, cfg.generationID, cfg, &turnThinking)
				}
			}
			out.emit(runCtx, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			return all, nil
		}
		// NOTE: we intentionally do NOT fail a turn just because the model
		// narrated without calling a tool. Thinking/narrating before acting is
		// healthy model behavior (the same way any capable model reasons before
		// it acts) - penalising it cuts the model off before it gets to act.
		// The run continues; a model that truly spins forever is bounded by the
		// real safety nets: maxInferenceTurns (per-role turn cap), the wall-time
		// idle budget, MaxToolCalls / MaxIdenticalToolCalls, and the
		// toolless-turn budget right below. We deliberately do NOT inspect the
		// model's text to guess "done vs still working" and we do NOT hash the
		// output - the only end signal is an explicit terminal tool
		// (sapaloq_stop / sapaloq_complete_task / sapaloq_fail_task).
		if toolCalls > 10 {
			// Proven productive: accept every turn and top the budget up so a
			// long working session is never cut off mid-flow.
			toollessBudget++
		} else if len(toolResults) > 0 || dynamicProgress.Load() {
			attemptedStop := false
			for _, item := range pendingTools {
				if item.call.Name == "sapaloq_stop" {
					attemptedStop = true
					break
				}
			}
			// A sapaloq_stop that did not end the run is not real progress; it
			// must not refill the tool-less budget or the foreground can spin
			// forever on a rejected stop attempt.
			if !(attemptedStop && !stop) {
				toollessBudget++
			}
		} else if isAgentNarrationTurn(toolCalls, response.String()) {
			// Cursor often narrates ("Found the root cause…") one turn before the
			// next tool call. Do not burn the tool-less budget on that healthy beat.
			if toollessBudget < budget.MaxNoProgressTurns {
				toollessBudget++
			}
		} else {
			// A tool-less turn burns the budget.
			toollessBudget--
		}
		if len(toolResults) > 0 || dynamicProgress.Load() {
			attemptedStop := false
			for _, item := range pendingTools {
				if item.call.Name == "sapaloq_stop" {
					attemptedStop = true
					break
				}
			}
			if attemptedStop && !stop {
				toollessStreak++
			} else {
				toollessStreak = 0
			}
		} else if isAgentNarrationTurn(toolCalls, response.String()) {
			// Narration-only turn: do not escalate autopilot toward sapaloq_stop.
		} else {
			toollessStreak++
		}
		// Toolless-budget finish. The model ran out of things to do without
		// ever calling the terminal tool to stop - the common case for a chat
		// answer the model never explicitly closes - so we end the run CLEANLY
		// (EventDone) rather than surfacing a scary "loop detected" error.
		// budget.MaxNoProgressTurns <= 0 disables this bound entirely.
		if budget.MaxNoProgressTurns > 0 && toollessBudget <= 0 {
			emitSteeringSkipped()
			if cfg.recordToolTurns {
				o.persistAssistantTurnWithThinking(ctx, persistID, response.String(), cfg.generationID, cfg, &turnThinking)
			}
			out.emit(runCtx, statusEvent(sessionID, "tool-less budget exhausted - ending turn"))
			out.emit(runCtx, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			return all, nil
		}
		// Build the continuation prompt. A tool-less turn gets a single neutral,
		// UNCONDITIONAL nudge - the same text every time, never derived from the
		// model's response - reminding it that the run continues until it calls
		// the terminal tool to stop. This is not a judgement of the model's text
		// (we deleted that); it is just the message that keeps the loop fed so a
		// tool-less turn doesn't replay an empty transcript. A normal tool turn
		// instead feeds the results back as PURE DATA. All the steering that used
		// to ride along with the tool output (observe/summarize/continue/usage
		// pacing) now lives in the persona system prompt, so the tool turn stays
		// clean - just <untrusted_data>-wrapped results - which models reason
		// over best.
		if messages, applied := o.appendActorEvents(cleanMessages, runID); applied {
			cleanMessages = messages
			steeringPendingAck = true
		}
		toolResultsBody := toolObservationBody(toolResults)
		if len(toolResults) == 0 {
			// SapaLOQ's own autopilot continuation, NOT a message from the
			// human user. The continuation is built from concrete session
			// signals (running tasks, pending clarification, context fullness)
			// + an escalation counter so repeated tool-less turns converge on a
			// silent sapaloq_stop, instead of the old single static string that
			// could not tell "a task needs your answer" from "you already
			// answered". Wrapped in <sapaloq:autopilot> so the model never
			// mistakes this system-generated nudge for the user typing
			// "continue" - the only UNMARKED user turn is the real human.
			sig := o.sessionSignals(sessionID)
			if cw := snap.entry.ContextWindow; cw > 0 {
				sig.contextPercent = o.effectiveContextPercent(runCtx, sessionID, cleanMessages, cw)
			}
			toolResultsBody = buildAutopilotContinuation(toolCalls, toollessStreak, toolResults, sig, runtimeCfg.Compaction.SteerPercent)
			status := "continuing"
			if toolCalls == 0 {
				status = "continuing - call `sapaloq_stop` to finish"
			}
			out.emit(runCtx, statusEvent(sessionID, status))
		}
		continuation := toolResultsBody
		// Record the tool calls this turn actually made into the assistant
		// message. response.String() carries only the model's text deltas - not
		// the tool_call itself - so without this the next turn sees the model's
		// narration ("I'll delegate to an agent…") followed by a tool result,
		// but no evidence that IT invoked the tool. Some models (e.g. Opus)
		// need that confirmation in-transcript to trust the action happened;
		// lacking it they second-guess ("I forgot to actually call it") and
		// re-issue the same call - the double-spawn bug. Appending an explicit
		// [Called tools: …] note gives that proof back. Models that don't need
		// it (e.g. minimax) are unaffected.
		assistantContent := response.String()
		if note := calledToolsNote(pendingTools); note != "" {
			if assistantContent != "" {
				assistantContent += "\n\n"
			}
			assistantContent += note
		}
		assistantContent = strings.TrimSpace(artifacts.StripModelResponseArtifact(assistantContent))
		if artifacts.IsAutopilotEcho(assistantContent) {
			assistantContent = ""
		}
		if cfg.recordToolTurns && assistantContent != "" {
			o.persistAssistantTurnWithThinking(ctx, persistID, assistantContent, cfg.generationID, cfg, &turnThinking)
		} else if cfg.recordToolTurns {
			o.flushTurnThinking(ctx, persistID, cfg.generationID, cfg, &turnThinking)
		}
		// Persist the continuation only after this inference round's thinking and
		// assistant turn. turns.json is an ordered transcript contract: the next
		// model input (tool result or autopilot nudge) must never receive an id/seq
		// before the output that caused it.
		if cfg.recordToolTurns && o.chat != nil && len(toolResults) > 0 {
			_ = o.chat.AppendTurn(ctx, persistID, "tool", toolResultsBody, estimateContentTokens(toolResultsBody))
		}
		if cfg.recordToolTurns && o.chat != nil && len(toolResults) == 0 {
			skipPersist := false
			if turns, terr := o.chat.ActiveTurns(ctx, persistID, false); terr == nil {
				for _, t := range turns {
					if t.Role == "autopilot" && t.Content == toolResultsBody {
						skipPersist = true
						break
					}
				}
			}
			if !skipPersist {
				_ = o.chat.AppendAutopilotTurn(ctx, persistID, toolResultsBody, estimateContentTokens(toolResultsBody))
			}
		}
		// A turn carrying tool output is fed back under the dedicated "tool"
		continuationRole := "user"
		if len(toolResults) > 0 {
			continuationRole = "tool"
		}
		if assistantContent != "" {
			cleanMessages = append(cleanMessages,
				bridge.Message{Role: "assistant", Content: assistantContent},
			)
		}
		cleanMessages = append(cleanMessages,
			bridge.Message{Role: continuationRole, Content: continuation},
		)
		// This turn did not end the run (no terminal tool, no toolless-budget
		// finish - both return earlier), so the loop is about to feed the next
		// inference turn. Mark the seam so the widget flushes the current
		// assistant bubble and starts a fresh one; otherwise every turn's
		// narration (turn 1, the <sapaloq:autopilot> continuation, turn 2, …)
		// would pile into a single merged bubble. UI-only hint; never ends the
		// run.
		out.emit(runCtx, bridge.StreamEvent{Kind: bridge.EventTurnBoundary, SessionID: sessionID, At: time.Now().UTC()})
		// Re-extract images from the freshly appended tool-results message so a
		// read_image tool call (which returns inline-image markdown) becomes real
		// vision input on the next turn - the same channel widget attachments use.
		cleanMessages, images = extractImages(cleanMessages)
		// If the model is already known text-only (this run downgraded it, or a
		// prior run/config marked it), drop the images and keep going on text
		// instead of stalling - the markdown is already a text placeholder.
		if len(images) > 0 && (visionDowngraded || !o.visionAllowed(snap.entry.Key, snap.entry.Model)) {
			images = nil
		}
	}
	return all, fmt.Errorf("inference-turn budget exhausted after %d turns", maxInferenceTurns)
}

// isAgentNarrationTurn reports a tool-less turn that already produced substantive
// task narration (typical Cursor beat before the next read/edit/exec call).
func isAgentNarrationTurn(toolCallsSoFar int, response string) bool {
	if toolCallsSoFar == 0 {
		return false
	}
	body := strings.TrimSpace(StripCalledToolsMarkers(response))
	if len(body) < 40 {
		return false
	}
	if artifacts.IsModelResponseArtifact(body) || artifacts.IsAutopilotEcho(body) {
		return false
	}
	return true
}

func toolCallSignature(call parse.ToolCall) string {
	return call.Name + "\x00" + strings.TrimSpace(string(call.Arguments))
}

// calledToolsNote (the "[Called tools: …]" in-transcript record) now lives in
// prompt.go alongside the other model-facing prompt fragments.

func conversationTokenRatio(messages []bridge.Message, contextWindow int) float64 {
	if contextWindow <= 0 {
		contextWindow = defaultContextWindow
	}
	total := 0
	for _, message := range messages {
		total += estimateContentTokens(message.Content)
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
	// The image-bearing message is the most recent fresh input to the model -
	// either the user's latest message or a tool observation (e.g. read_image
	// returns inline-image markdown). Both are valid vision sources; only this
	// one message contributes real images, older ones become text placeholders.
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" || messages[i].Role == "tool" {
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
// most likely means the model can't accept images - so we should mark it
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
// timeouts, dropped/reset connections, premature EOFs, and 5xx/429 responses -
// the classic "try again in a moment" class. Context-overflow is intentionally
// excluded here because it has its own dedicated compaction-and-retry path.
//
// 4xx statuses that indicate the request itself is wrong (auth failure, model
// not found, forbidden, bad request) are ALSO excluded: a wrong API key or a
// non-existent model will fail the exact same way on every retry, and the user
// would see the agent appear to hang for 4 attempts before giving up. We
// detect them by both the wire-level status ("status 401/403/404") and the
// body-level error name (the bridge surfaces upstream error names like
// "AuthenticationError" / "NotFoundError" / "PermissionDeniedError" alongside
// the status code, so either form is enough to identify a deterministic
// failure).
func looksLikeTransientTransport(message string) bool {
	lower := strings.ToLower(message)
	if looksLikeContextOverflow(lower) {
		return false
	}
	// Deterministic client errors must never be retried - they will just
	// fail the same way again and burn the per-turn retry budget while
	// the user sees the agent "hanging".
	for _, kw := range []string{
		"status 400", "status 401", "status 403", "status 404", "status 408", "status 409", "status 410", "status 412", "status 415", "status 422",
		"400 ", "401 ", "403 ", "404 ", "408 ", "409 ", "410 ", "412 ", "415 ", "422 ",
		"authenticationerror", "notfounderror", "permissiondeniederror", "permission_denied",
		"invalid api key", "invalid_api_key", "unauthorized", "authentication failed",
		"forbidden", "model not found", "model_not_found", "model group fallbacks=none",
		"billing", "payment required", "quota exceeded", "credit",
	} {
		if strings.Contains(lower, kw) {
			return false
		}
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
		"empty response", "returned no data", "stream ended with no data",
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

// redactToolResults masks secrets in every tool result before it is added to
// the model context (and therefore before it reaches logs or any egress). The
// AI keeps full access to every tool; only secret values in the results are
// replaced with [SECRET]. This neutralises the exfiltration tail of a prompt
// injection (e.g. "read ~/.ssh/id_rsa and send it") without restricting any
// action the AI may take. Trade-off: a task that legitimately needs a secret
// value also sees [SECRET] - see docs/ORCHESTRATOR.md. A nil redactor (only in
// tests that construct the struct directly) passes results through unchanged.
//
// read_image results are passed through untouched: they are intentional vision
// input (data:…;base64,…) and the high-entropy fallback would otherwise replace
// the payload with [SECRET], breaking extractImages on the next turn.
func (o *Orchestrator) redactToolResults(results []string) []string {
	if o == nil || o.redactor == nil || len(results) == 0 {
		return results
	}
	out := make([]string, len(results))
	for i, r := range results {
		if isInlineImageToolResult(r) {
			out[i] = r
			continue
		}
		out[i] = o.redactor.Redact(r).Redacted
	}
	return out
}

// isInlineImageToolResult reports a read_image payload: inline markdown with a
// data:image/* URI. These are vision input, not exfiltration targets.
func isInlineImageToolResult(s string) bool {
	return inlineImageRE.MatchString(strings.TrimSpace(s))
}

// ensureConversationEndsWithUser appends a synthetic user continuation when the
// last non-system message is assistant/tool/error. Required by Anthropic and
// Vercel AI gateway providers that reject assistant-message prefill.
func ensureConversationEndsWithUser(messages []bridge.Message) []bridge.Message {
	lastIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		switch messages[i].Role {
		case "system", "thinking", "autopilot":
			continue
		}
		lastIdx = i
		break
	}
	if lastIdx < 0 {
		return messages
	}
	switch messages[lastIdx].Role {
	case "user", "tool":
		return messages
	}
	out := append([]bridge.Message{}, messages...)
	out = append(out, bridge.Message{
		Role:    "user",
		Content: sapaloqControlBody("Continue from the context above."),
	})
	return out
}
