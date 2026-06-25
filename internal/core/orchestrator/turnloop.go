package orchestrator

import (
	"context"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// turnloop.go is the SINGLE inference engine shared by the chat (Ask) loop and
// every sub-agent role (planner, task-runner/agent, scribe). Historically chat
// and sub-agents had two independent multi-turn loops; the sub-agent copy was
// missing the chat loop's budgets, loop-detection, compaction and clean
// error/stream handling, so it accumulated a long tail of stall/“kepentok”
// bugs that were patched over and over. Unifying them means a fix or guard
// written once protects both, and planner/agent differ from chat ONLY by:
//   - the system prompt (already baked into the messages slice),
//   - the offered tool set,
//   - how a tool call is dispatched + how a turn is allowed to end,
//   - where stream events go (chat: live channel; sub-agent: progress + heartbeat).

// turnOutcome is the normalized result of dispatching one tool call. It is the
// common shape both handleAskTool (chat) and handleSubAgentTool (sub-agent)
// are adapted to, so runTurnLoop never needs to know which role it serves.
type turnOutcome struct {
	// text is the tool result fed back to the model on the next turn. Empty
	// when the tool produced no model-visible output.
	text string
	// handled marks the call as a recognized tool that produced a result turn
	// (counts as progress). Unhandled calls are ignored. Mirrors the chat
	// loop's askToolResult.handled so behavior is identical.
	handled bool
	// stop ends the loop after this turn (chat: sapaloq_stop; sub-agent: a
	// terminal tool such as sapaloq_complete_task/sapaloq_fail_task).
	stop bool
}

// turnSink decouples the loop from its output. The chat sink streams events to
// the live widget channel; the sub-agent sink records them to the progress
// JSONL.
//
// NOTE: worker liveness is NO LONGER driven from here. Heartbeats used to fire
// from beat() on every delta/tool, which meant a synchronous tool (exec ≤600s),
// a slow time-to-first-byte, or a silent stream produced NO heartbeat and the
// watchdog force-killed an agent that was actually fine - the recurring stall.
// Liveness is now structural: runBackgroundTask runs a heartbeat ticker for as
// long as the worker goroutine lives, so the watchdog only catches a genuinely
// dead/wedged goroutine. beat() therefore only annotates the current phase.
type turnSink interface {
	// emit forwards one stream event to wherever this run observes output.
	emit(ctx context.Context, ev bridge.StreamEvent)
	// beat annotates the current phase for observability. It does NOT keep the
	// worker alive (the ticker does); it is a no-op for chat.
	beat(phase string)
}

// turnConfig parameterizes one run of the shared engine.
type turnConfig struct {
	sessionID string
	// runID is the stable actor identity used to correlate tool jobs and
	// steering/decision events. It may equal sessionID for a foreground run.
	runID string
	// tools is the declared-tool surface offered to the model this run.
	tools []string
	// dispatch executes one tool call and returns its normalized outcome.
	dispatch func(ctx context.Context, call parse.ToolCall) turnOutcome
	// sink receives every stream event (+ heartbeats for sub-agents).
	sink turnSink
	// finishOnNoTool ends the run when a turn produces no tool call. Chat and
	// planner finish naturally this way; an executor (task-runner) must instead
	// signal completion via a terminal tool, so it keeps looping (bounded by
	// the budgets/loop-guards) until it does or the budget is exhausted.
	finishOnNoTool bool
	// continueUntilNoOp flips the tool-less polarity for a finishOnNoTool role
	// (experimental, opt-in). When false (default) a tool-less turn finishes
	// the run immediately - which mis-reads a high-reasoning model that
	// narrates its next step in one response and emits the tool call in the
	// NEXT (planner "done" before it actually planned). When true, a tool-less
	// turn does NOT end the run by itself: the model must reply with exactly
	// the NO_OP sentinel to signal completion (the OpenClaw HEARTBEAT_OK /
	// Copilot NO_OP pattern). Any other tool-less text is treated as "still
	// working" and the loop injects an internal "continue" follow-up, bounded
	// by maxContinueNudges so a model that never says NO_OP still terminates.
	// Ignored unless finishOnNoTool is also true.
	continueUntilNoOp bool
	// continueOnIntent keeps a finishOnNoTool role going when a tool-less turn
	// clearly NARRATES an intent to keep acting (e.g. "Let me check the next
	// file." / "Saya akan membaca…") but the model deferred the actual tool call
	// to its NEXT response - the exact high-reasoning pattern (opus-class) that
	// otherwise made the run finish prematurely after the narration turn. Unlike
	// continueUntilNoOp it does NOT require the model to learn a NO_OP sentinel:
	// a turn that reads like a FINAL answer (no continuation cue) still finishes
	// immediately, so normal chat answers are not delayed. A turn that reads
	// like "still working" gets one internal nudge to act or finalize, bounded
	// by maxContinueNudges so a model that narrates forever still terminates.
	// Ignored unless finishOnNoTool is also true; orthogonal to and checked
	// after continueUntilNoOp.
	continueOnIntent bool
	// thinkingOut, when non-nil, accumulates reasoning text for persistence as
	// a show-only chat "thinking" turn. Sub-agents leave this nil.
	thinkingOut *strings.Builder
	// recordToolTurns persists tool-result turns to the chat store for context
	// accounting. Chat-only.
	recordToolTurns bool
	// maxInferenceTurns overrides the continuation budget's turn cap when > 0
	// (sub-agent roles use roleMaxTurns); 0 means use the budget value.
	maxInferenceTurns int
}

// chatSink streams events to the live chat channel. beat is a no-op because the
// chat run is observed in real time by the widget; it has no watchdog.
type chatSink struct {
	o   *Orchestrator
	out chan<- bridge.StreamEvent
}

func (s chatSink) emit(ctx context.Context, ev bridge.StreamEvent) { s.o.emit(ctx, s.out, ev) }
func (s chatSink) beat(string)                                     {}

// subagentSink records events to the per-task progress JSONL. It does NOT touch
// the worker heartbeat (the structural ticker in runBackgroundTask owns that);
// beat() only updates the phase label for observability.
type subagentSink struct {
	o      *Orchestrator
	taskID string
}

func (s *subagentSink) emit(_ context.Context, ev bridge.StreamEvent) {
	if ev.SessionID == "" {
		ev.SessionID = s.taskID
	}
	// Bridge EventDone ends one inference response, not the background task;
	// persisting it made the progress JSONL look falsely terminal. Task
	// lifecycle is recorded separately as EventTaskUpdate.
	if ev.Kind != bridge.EventDone {
		_ = s.o.progress.Append(s.taskID, ev)
	}
}

// beat updates only the phase label (liveness is owned by the ticker). Passing
// an empty phase here would clobber nothing, so we skip the no-op case.
func (s *subagentSink) beat(phase string) {
	if phase == "" {
		return
	}
	s.o.workers.setPhase(s.taskID, phase)
}

// noOpSentinel is the explicit "I am done" signal a continueUntilNoOp role must
// emit to end its run. It is matched case-insensitively as the WHOLE response
// (after trimming) so it is unambiguous and cannot be confused with a sentence
// that merely mentions NO_OP. Modelled on OpenClaw's HEARTBEAT_OK and the
// Copilot NO_OP stop convention.
const noOpSentinel = "NO_OP"

// isNoOpResponse reports whether a model's tool-less text response is the bare
// NO_OP sentinel (the completion signal), tolerating surrounding whitespace and
// trivial casing/punctuation. It is deliberately strict about being the ENTIRE
// response: a turn that says "I won't NO_OP yet because…" keeps working, only a
// turn whose whole content is NO_OP terminates.
func isNoOpResponse(text string) bool {
	trimmed := strings.TrimSpace(text)
	// Allow a single trailing period and surrounding code fences/quotes a model
	// might wrap a lone token in, but nothing more substantive.
	trimmed = strings.Trim(trimmed, "`\"'.")
	trimmed = strings.TrimSpace(trimmed)
	return strings.EqualFold(trimmed, noOpSentinel)
}

// continuationCues are phrases that signal a model is about to keep acting
// rather than delivering a final answer. They are matched ONLY against the tail
// of a tool-less turn (the model's last sentence), because a continuation cue
// that appears mid-answer ("Let me explain how X works: …") is part of an
// explanation, whereas one that ENDS the turn ("…how nft mode is wired. Let me
// check.") is a deferred next step. English + Indonesian, lower-cased.
var continuationCues = []string{
	"let me ",
	"let's ",
	"lets ",
	"i'll ",
	"i will ",
	"i am going to ",
	"i'm going to ",
	"next, i",
	"next i",
	"now i'll",
	"now i will",
	"now let me",
	"now, let me",
	// Indonesian
	"saya akan ",
	"saya cek ",
	"saya periksa ",
	"mari ",
	"selanjutnya",
	"berikutnya",
	"sekarang saya",
	"coba saya ",
	"akan saya ",
}

// looksLikeContinuationIntent reports whether a tool-less turn reads as "still
// working - next step coming" rather than a final answer. It is deliberately
// conservative: it inspects only the LAST non-empty line and requires either a
// recognised continuation cue there, or that the whole response ends with a
// dangling ":" / "…" / "..." (a model that is about to enumerate or act). A
// turn that does not match is treated as a final answer and finishes normally,
// so ordinary chat replies are never delayed.
func looksLikeContinuationIntent(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	// A bare NO_OP is a completion signal, not a continuation.
	if isNoOpResponse(trimmed) {
		return false
	}
	// Dangling enumerations/actions: "…", "...", or a trailing colon.
	if strings.HasSuffix(trimmed, "…") || strings.HasSuffix(trimmed, "...") || strings.HasSuffix(trimmed, ":") {
		return true
	}
	// Inspect the last non-empty line only.
	lines := strings.Split(trimmed, "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			last = strings.ToLower(s)
			break
		}
	}
	if last == "" {
		return false
	}
	// Cue at the START of the final sentence is the strongest signal, but a cue
	// anywhere in the final line still indicates a deferred next step (the
	// observed failure ended with "… Let me check the aggregate filter logic
	// and how nft mode is wired.").
	for _, cue := range continuationCues {
		if strings.Contains(last, cue) {
			return true
		}
	}
	return false
}
