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
	// A run NEVER ends because a turn produced no tool call. The absence of a
	// tool call is not a completion signal - it is just a turn that narrated,
	// reasoned, or answered without acting, which every capable model does. The
	// ONLY explicit end signal is a terminal tool (chat: sapaloq_stop;
	// sub-agent: sapaloq_stop / sapaloq_complete_task / sapaloq_fail_task)
	// surfaced as turnOutcome.stop. Everything else keeps looping, bounded
	// solely by the structural budgets (turn cap, idle wall-time, MaxToolCalls,
	// toolless-turn budget). This deliberately drops the old "no-tool = stop" polarity and its
	// tebak-tebakan tambalan (continueUntilNoOp/NO_OP sentinel, continueOnIntent
	// narration heuristics): the logic was sound but it relied on the model
	// behaving a way models do not reliably behave, so it stopped at the wrong
	// place. We do not judge the model's text to decide continuation, and we do
	// not use a second model to judge it either.
	//
	// thinkingOut, when non-nil, accumulates reasoning text for persistence as
	// a show-only chat "thinking" turn. Sub-agents leave this nil.
	thinkingOut *strings.Builder
	// recordToolTurns persists tool-result turns to the chat store for context
	// accounting. Chat-only.
	recordToolTurns bool
	// maxInferenceTurns overrides the continuation budget's turn cap when > 0
	// (sub-agent roles use roleMaxTurns); 0 means use the budget value.
	maxInferenceTurns int
	// suppressHeadroomCompaction skips the 95% headroom force-checkpoint path.
	// Set for nested compaction sub-runs so they cannot recursively spawn another
	// full turn loop (which would duplicate the entire message slice in RAM).
	suppressHeadroomCompaction bool
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
