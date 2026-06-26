package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
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
		thinkingOut:     thinkingOut,
		recordToolTurns: true,
		dispatch: func(ctx context.Context, call parse.ToolCall) turnOutcome {
			res := o.handleAskTool(ctx, snap, out, sessionID, fallbackTask, call)
			return turnOutcome{text: res.text, handled: res.handled, stop: res.stop}
		},
	}
	return o.runTurnLoop(ctx, snap, fallbackTask, messages, cfg)
}

// runConversationActorSink is the turnSink-based variant used by the forced
// checkpoint turn, which is launched from inside an already-running turn loop
// that only holds a turnSink (not the raw channel). It wires a buffered channel
// that drains into the sink so handleAskTool (which expects a chan) works
// unchanged, and so the live EventCheckpoint still reaches the real UI.
func (o *Orchestrator) runConversationActorSink(ctx context.Context, snap providerSnapshot, sink turnSink, sessionID, runID, fallbackTask string, messages []bridge.Message, thinkingOut *strings.Builder) (strings.Builder, error) {
	ch := make(chan bridge.StreamEvent, 64)
	drainCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer cancel()
		for {
			select {
			case <-drainCtx.Done():
				return
			case ev := <-ch:
				sink.emit(drainCtx, ev)
			}
		}
	}()
	cfg := turnConfig{
		sessionID:       sessionID,
		runID:           runID,
		tools:           askTools,
		sink:            chatSink{o: o, out: ch},
		thinkingOut:     thinkingOut,
		recordToolTurns: true,
		dispatch: func(ctx context.Context, call parse.ToolCall) turnOutcome {
			res := o.handleAskTool(ctx, snap, ch, sessionID, fallbackTask, call)
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

	// Bounds how many consecutive turns may emit tool calls that produce no
	// usable result (a non-native model leaking malformed inline tool calls).
	// Instead of mistaking such a turn for a clean tool-less finish and ending
	// the run prematurely, we nudge the model to retry one call at a time; this
	// caps that nudge so a model that can never produce a valid call still
	// finishes instead of looping forever.
	malformedToolTurns := 0
	const maxMalformedToolTurns = 3

	// maxInferenceTurns: a positive value caps the loop; a NEGATIVE value means
	// UNLIMITED - the loop is then bounded only by the real anomaly guards
	// (wall-time, no-progress, identical-tool, tool-call budgets). cfg overrides
	// the config default when set to any non-zero value (so an explicit -1 from a
	// role survives instead of falling back to the bounded config default).
	maxInferenceTurns := budget.MaxInferenceTurns
	if cfg.maxInferenceTurns != 0 {
		maxInferenceTurns = cfg.maxInferenceTurns
	}
	unlimitedTurns := maxInferenceTurns < 0
	turnBudgetLabel := fmt.Sprintf("%d", maxInferenceTurns)
	if unlimitedTurns {
		turnBudgetLabel = "∞"
	}

	for inferenceTurn := 1; unlimitedTurns || inferenceTurn <= maxInferenceTurns; inferenceTurn++ {
		if err := runCtx.Err(); err != nil {
			out.emit(context.Background(), bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			if ctx.Err() != nil {
				return all, nil
			}
			return all, fmt.Errorf("run stalled: no activity for %d minutes", budget.MaxWallTimeMinutes)
		}
		if control := actorEventsPrompt(o.drainActorEvents(runID)); control != "" {
			cleanMessages = append(cleanMessages, bridge.Message{Role: "user", Content: control})
		}
		// Heartbeat at the top of every turn so the health watchdog can tell a
		// genuinely-working agent (advancing turns) from a wedged goroutine.
		// Advancing a turn is progress - reset the inactivity deadline.
		resetIdle()
	out.beat(fmt.Sprintf("inference turn %d/%s", inferenceTurn, turnBudgetLabel))
	if runtimeCfg.Compaction.UseCheckpoints {
		// LLM-driven checkpoint compaction: at the headroom threshold
		// (default 5% remaining) inject a blocking forced-compaction turn
		// that steers the model to call sapaloq_compact_session, then rebuild
		// cleanMessages from the new checkpoint summary + anchored tail. The
		// old heuristic compactConversationMessages path is skipped entirely.
		if o.contextHeadroomReached(cleanMessages, snap.entry.ContextWindow, runtimeCfg.Compaction.HeadroomPercent) {
			res, ok, ferr := o.forceCheckpoint(runCtx, snap, out, sessionID, fallbackTask, "force_headroom", cleanMessages)
			if ferr != nil {
				return all, ferr
			}
			if ok {
				rebuilt, rerr := o.rebuildAfterCheckpoint(ctx, sessionID, cleanMessages)
				if rerr == nil {
					cleanMessages = rebuilt
					lastCompactedMessageCount = len(cleanMessages)
				}
				_ = res
			} else {
				// Model refused within the retry budget: surface an actionable
				// error rather than silently falling back to heuristic compact.
				return all, fmt.Errorf("context at %d%% and model did not compact within %d retries; run /compaction or shorten the conversation", o.contextPercent(cleanMessages, snap.entry.ContextWindow), runtimeCfg.Compaction.MaxForceRetries)
			}
		}
	} else if shouldCompactConversation(cleanMessages, snap.entry.ContextWindow, runtimeCfg.Compaction.BackgroundThreshold) &&
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
				return all, fmt.Errorf("run stalled: no activity for %d minutes", budget.MaxWallTimeMinutes)
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
				out.emit(runCtx, ev)
				if ev.ToolCall != nil {
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
				if thinkingOut != nil {
					thinkingOut.WriteString(ev.Delta)
				}
				out.beat(fmt.Sprintf("thinking turn %d/%s", inferenceTurn, turnBudgetLabel))
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
		// The stream for this attempt has ended (done, channel close, or
		// cancellation). Release any text the calledToolsFilter was withholding
		// as a possible "[Called tools: …]" marker that turned out to be
		// ordinary text; an unterminated marker body is intentionally dropped.
		if tail := ctFilter.flush(); tail != "" {
			response.WriteString(tail)
			all.WriteString(tail)
			out.emit(runCtx, bridge.StreamEvent{Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: tail, At: time.Now().UTC()})
		}
		if len(pendingTools) > 0 && !retryTextOnly && !retryCompacted && !retryTransport && !hadError {
			results, batchStop := o.executeToolBatch(runCtx, runID, sessionID, pendingTools)
			toolResults = append(toolResults, o.redactToolResults(results)...)
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
		if runtimeCfg.Compaction.UseCheckpoints {
			// Overflow 400: run a forced LLM-authored checkpoint turn instead of
			// the heuristic compactConversationMessages retry. On success,
			// rebuild cleanMessages from the new checkpoint + anchored tail and
			// re-run this same turn. If the model refuses within the retry
			// budget, surface the original overflow error (no silent heuristic
			// fallback in v1).
			_, ok, ferr := o.forceCheckpoint(runCtx, snap, out, sessionID, fallbackTask, "force_overflow", cleanMessages)
			if ferr != nil {
				return all, ferr
			}
			if !ok {
				return all, fmt.Errorf("context overflow and model did not compact within %d retries: %s", runtimeCfg.Compaction.MaxForceRetries, lastErr)
			}
			rebuilt, rerr := o.rebuildAfterCheckpoint(ctx, sessionID, cleanMessages)
			if rerr != nil || len(rebuilt) >= len(cleanMessages) {
				return all, fmt.Errorf("context overflow and checkpoint did not shrink context: %s", lastErr)
			}
			forcedCompactions++
			cleanMessages = rebuilt
			lastCompactedMessageCount = len(cleanMessages)
			cleanMessages, images = extractImages(cleanMessages)
			if len(images) > 0 && (visionDowngraded || !o.visionAllowed(snap.entry.Key, snap.entry.Model)) {
				images = nil
			}
			out.emit(runCtx, statusEvent(sessionID, "working"))
			inferenceTurn--
			continue
		}
		// Legacy heuristic fallback (useCheckpoints=false): force one
		// compaction pass and re-run this same turn against the shrunken
		// history. The failed attempt is already cancelled. If compaction
		// can't shrink further (already minimal), recovery is impossible -
		// surface the original overflow error rather than loop.
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
				return all, fmt.Errorf("run stalled: no activity for %d minutes", budget.MaxWallTimeMinutes)
			case <-time.After(backoff):
			}
			inferenceTurn--
			continue
		}
		// A turn that finished without a transport error clears the retry
		// budget, so an occasional blip during a long run doesn't accumulate.
		transportRetries = 0
		if hadError {
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
			// runSubAgentLoop keys "failed vs done" off this returned error.
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
				"markers. Please issue ONE tool call at a time using the proper tool-call " +
				"format, or, if no tool is needed, just answer in plain text."
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
		// The ONLY explicit end signal is a terminal tool (chat: sapaloq_stop;
		// sub-agent: sapaloq_complete_task/sapaloq_fail_task), surfaced here as
		// `stop`. A tool-less turn NEVER ends the run - the absence of a tool
		// call is not a completion signal, it is just a turn that narrated,
		// reasoned, or answered without acting. We do NOT inspect the model's
		// text to guess "done vs still working" (the old NO_OP/intent
		// tebak-tebakan), and we do NOT use a second model to judge it. The run
		// simply continues; the structural budgets below (turn cap, idle
		// wall-time, MaxToolCalls, no-progress hash) are the only bounds.
		if stop {
			out.emit(runCtx, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			return all, nil
		}
		// NOTE: we intentionally do NOT fail a turn just because the model
		// narrated without calling a tool. Thinking/narrating before acting is
		// healthy model behavior (the same way any capable model reasons before
		// it acts) - penalising it cuts the model off before it gets to act.
		// A model that truly spins forever is still bounded by the real safety
		// nets: maxInferenceTurns (per-role turn cap), the wall-time budget,
		// MaxToolCalls, and the no-progress hash guard right below (which fires
		// only on genuinely identical, zero-progress turns). The outcome is
		// normalized before hashing so a model that paraphrases itself with
		// only whitespace / punctuation / "[Called tools: …]" echo differences
		// is still recognized as making no progress (the old exact-hash guard
		// let such turns reset the counter and spin until the turn cap).
		outcome := fmt.Sprintf("%x", sha256.Sum256([]byte(normalizeOutcomeForHash(response.String())+"\x00"+strings.Join(toolResults, "\x00"))))
		if outcome == lastOutcome {
			noProgressTurns++
		} else {
			lastOutcome = outcome
			noProgressTurns = 0
		}
		// No-progress finish. Now that the ONLY explicit end signal is a
		// terminal tool, a model that keeps producing the SAME output without
		// any new tool result is not "looping" - it has simply run out of things
		// to do and did not call sapaloq_stop. That is the common case (a normal
		// chat answer the model never explicitly closes), so we end the run
		// CLEANLY (EventDone) rather than surfacing a scary "loop detected"
		// error at the wrong place. Genuine tool-call thrash is bounded
		// separately by MaxToolCalls / MaxIdenticalToolCalls; runaway narration
		// is bounded by the turn cap + idle wall-time. budget.MaxNoProgressTurns
		// <= 0 disables this finish entirely (observe raw model behavior).
		if budget.MaxNoProgressTurns > 0 && noProgressTurns >= budget.MaxNoProgressTurns {
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
				sig.contextPercent = (estimateMessagesTokens(cleanMessages) * 100) / cw
			}
			toolResultsBody = buildAutopilotContinuation(inferenceTurn, toolResults, sig, runtimeCfg.Compaction.SteerPercent)
			out.emit(runCtx, statusEvent(sessionID, "continuing - call `sapaloq_stop` to finish"))
		}
		// Persist tool results as a "tool" turn so they count toward context
		// usage and auto-compaction. These messages ARE sent to the model (they
		// are appended to cleanMessages below and replayed via contextMessages;
		// the wire layer maps the internal "tool" role to a role the upstream
		// API accepts), so leaving them unrecorded made ContextUsage
		// under-count and auto-compact trigger too late. Use the outer ctx (not
		// the cancelable runCtx) so a wall-time timeout does not drop the
		// audit/accounting record. Chat-only (recordToolTurns).
		if cfg.recordToolTurns && o.chat != nil && len(toolResults) > 0 {
			_ = o.chat.AppendTurn(ctx, sessionID, "tool", toolResultsBody, estimateTextTokens(toolResultsBody))
		}
		// Persist a tool-less autopilot continuation as a dedicated "autopilot"
		// turn so it counts toward ContextUsage / auto-compaction accounting
		// (it occupies real context on the next turn) WITHOUT being replayed to
		// the model from history (the live in-run cleanMessages already carries
		// it) and WITHOUT surfacing as a chat bubble (the UI skips "autopilot"
		// like it skips "thinking"). Use the outer ctx so a wall-time timeout
		// does not drop the accounting record. Chat-only (recordToolTurns).
		if cfg.recordToolTurns && o.chat != nil && len(toolResults) == 0 {
			_ = o.chat.AppendTurn(ctx, sessionID, "autopilot", toolResultsBody, estimateTextTokens(toolResultsBody))
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
		// A turn carrying tool output is fed back under the dedicated "tool"
		// role so the model can tell an observation apart from a user request
		// (the wire layer maps it to an API-accepted role). A tool-less
		// autopilot continuation goes under "user" (the only role left once
		// tool/system are unavailable mid-conversation) but its CONTENT is
		// wrapped in <sapaloq:autopilot> by sapaloqControlBody above, so the
		// model still distinguishes this system-generated nudge from a real
		// human "user" turn - the role is "user", the marker says "not human".
		continuationRole := "user"
		if len(toolResults) > 0 {
			continuationRole = "tool"
		}
		cleanMessages = append(cleanMessages,
			bridge.Message{Role: "assistant", Content: assistantContent},
			bridge.Message{Role: continuationRole, Content: continuation},
		)
		// This turn did not end the run (no terminal tool, no no-progress
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

func toolCallSignature(call parse.ToolCall) string {
	return call.Name + "\x00" + strings.TrimSpace(string(call.Arguments))
}

// normalizeOutcomeForHash collapses the surface variation that does NOT
// constitute real progress, so the no-progress hash treats paraphrases as
// identical:
//   - strips echoed "[Called tools: …]" / "[Tool: …]" notes (the same markers
//     calledToolsFilter drops from the visible stream; a model imitating them
//     is not making progress),
//   - collapses all runs of whitespace to single spaces,
//   - trims surrounding whitespace and strips common trailing punctuation
//     variants so "Done." / "Done…" / "Done!" hash the same.
//
// It never inspects semantics - it is a pure surface normalization, so it
// cannot mistake a genuinely different answer for a repeat (different words
// still hash differently). The goal is only to stop the counter from resetting
// on whitespace / punctuation / marker-echo drift.
func normalizeOutcomeForHash(s string) string {
	// Drop called-tools / tool marker spans the same way the visible-stream
	// filter does: from the opening "[Called tools: " / "[Tool: " to the next
	// "]".
	for _, m := range calledToolsMarkers {
		for {
			i := strings.Index(s, m)
			if i < 0 {
				break
			}
			j := strings.IndexByte(s[i:], ']')
			if j < 0 {
				// No closer in this fragment; drop to end so a half-marker
				// echo does not keep the turn "different".
				s = s[:i]
				break
			}
			s = s[:i] + s[i+j+1:]
		}
	}
	// Collapse whitespace runs to a single space and trim. Fields handles
	// tabs/newlines/multiple spaces uniformly.
	s = strings.Join(strings.Fields(s), " ")
	// Strip a small set of trailing punctuation variants that models emit
	// interchangeably at the end of an otherwise-identical statement, then
	// re-trim any whitespace the punctuation was hugging.
	s = strings.TrimRight(s, ".…!?，。！？")
	s = strings.TrimSpace(s)
	return s
}

// calledToolsNote (the "[Called tools: …]" in-transcript record) now lives in
// prompt.go alongside the other model-facing prompt fragments.

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
func (o *Orchestrator) redactToolResults(results []string) []string {
	if o == nil || o.redactor == nil || len(results) == 0 {
		return results
	}
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = o.redactor.Redact(r).Redacted
	}
	return out
}
