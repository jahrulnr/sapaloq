package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// newTestOrchestrator builds an Orchestrator with a real worker registry and
// progress writer rooted under a temp dir, so sub-agent runs that heartbeat and
// append progress behave like production (instead of relying on nil guards).
func newTestOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	return &Orchestrator{
		memoryDir: dir,
		cfg:       config.Config{},
		workers:   newWorkerRegistry(filepath.Join(dir, "workers")),
		progress:  ProgressWriter{Dir: filepath.Join(dir, "progress")},
	}
}

// TestTaskRunnerRecoversFromEmptyStream covers the silent-truncation variant of
// the recurring stall bug: the stream connects and closes producing no text and
// no tool call. The UNIFIED engine simply loops (a tool-less turn is allowed,
// not a failure) with a plain continuation reminder - so an empty/truncated turn
// self-heals on the next turn instead of wedging the worker.
func TestTaskRunnerRecoversFromEmptyStream(t *testing.T) {
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		// Turn 1: completely empty (only the auto-appended EventDone).
		{},
		// Turn 2: it actually finishes.
		{toolCallEvent("sapaloq_complete_task", map[string]any{"summary": "Recovered."})},
	}}
	o := newTestOrchestrator(t)
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-empty", Role: "task-runner", Status: "in_progress", Task: "do it"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "done" {
		t.Fatalf("status = %q, want done (recovered via nudge after empty stream)", rec.Status)
	}
	if rec.Result != "Recovered." {
		t.Fatalf("result = %q, want the complete_task summary", rec.Result)
	}
	if fake.call < 2 {
		t.Fatalf("expected the empty turn to be followed by another (>=2 Complete calls), got %d", fake.call)
	}
}

// alwaysErrorBridge emits a fixed mid-stream EventError on every call, modelling
// a provider that is genuinely down for the duration of the run.
type alwaysErrorBridge struct {
	err   string
	calls int
}

func (b *alwaysErrorBridge) ID() string              { return "always-error" }
func (b *alwaysErrorBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *alwaysErrorBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.calls++
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		out <- bridge.StreamEvent{Kind: bridge.EventError, Error: b.err}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// TestTaskRunnerRetriesTransientThenSurfaces confirms a transient transport
// error (slow TTFB / timeout / 5xx) is RETRIED a bounded number of times rather
// than failing the whole task on the first blip - and that a provider which
// stays down still surfaces a failure instead of retrying forever.
func TestTaskRunnerRetriesTransientThenSurfaces(t *testing.T) {
	orig := transportRetryBaseBackoff
	transportRetryBaseBackoff = 0 // run instantly
	defer func() { transportRetryBaseBackoff = orig }()

	fake := &alwaysErrorBridge{err: `Post "https://api.tokenrouter.com/v1/chat/completions": net/http: timeout awaiting response headers`}
	o := newTestOrchestrator(t)
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-err", Role: "task-runner", Status: "in_progress", Task: "build a site"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "failed" {
		t.Fatalf("status = %q, want failed (provider stayed down after retries)", rec.Status)
	}
	if rec.Error == "" {
		t.Fatalf("a persistent transport error must record a concrete failure reason")
	}
	// 1 initial attempt + maxTransportRetries (4) retries = 5 calls. It must
	// have retried (not failed on the first error) but also be bounded.
	if fake.calls < 2 {
		t.Fatalf("transient error was not retried at all: %d call(s)", fake.calls)
	}
	if fake.calls > 6 {
		t.Fatalf("transport retries not bounded: %d calls", fake.calls)
	}
}

// TestPlannerSurfacesProviderError is the regression for the "halu sukses" bug
// seen in the field (orch-task progress: a planner hit a non-recoverable
// provider 500 yet the task was recorded `done`/"Selesai." with no plan.md, so
// Ask narrated a plan that never existed). A planner whose only LLM call fails
// non-recoverably must end `failed` with the provider error - never `done`.
// The error string mirrors the real Blackbox gateway 500 (carries
// "Model Group Fallbacks=None", which is classified non-transient so it is NOT
// retried - it surfaces immediately via the hadError path).
func TestPlannerSurfacesProviderError(t *testing.T) {
	fake := &alwaysErrorBridge{err: `provider-bridge: upstream status 500: {"error":{"message":"blackbox.Error: InternalServerError: Vercel_ai_gatewayException - Connection error.. Received Model Group=blackboxai/anthropic/claude-opus-4.8\nAvailable Model Group Fallbacks=None","type":"None","param":"None","code":"500"}}`}
	o := newTestOrchestrator(t)
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-planner-500", Role: "planner", Status: "in_progress", Task: "bikin web profile di /tmp/profile"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "failed" {
		t.Fatalf("status = %q, want failed (planner hit a non-recoverable provider 500)", rec.Status)
	}
	if rec.Error == "" {
		t.Fatalf("a failed planner must record the provider error as the reason")
	}
	if !strings.Contains(rec.Error, "500") {
		t.Fatalf("failure reason should carry the provider error, got %q", rec.Error)
	}
	// It must NOT have been retried (non-transient): exactly one upstream call.
	if fake.calls != 1 {
		t.Fatalf("non-transient 500 must not be retried, got %d call(s)", fake.calls)
	}
}

// TestTaskRunnerRecoversFromTransientError confirms a turn that hits a transient
// transport error and then succeeds on retry completes normally - one blip does
// not doom the task.
func TestTaskRunnerRecoversFromTransientError(t *testing.T) {
	orig := transportRetryBaseBackoff
	transportRetryBaseBackoff = 0
	defer func() { transportRetryBaseBackoff = orig }()

	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		// Turn 1: transient transport error (a slow provider).
		{{Kind: bridge.EventError, Error: "net/http: timeout awaiting response headers"}},
		// Retry of turn 1: it succeeds and finishes.
		{toolCallEvent("sapaloq_complete_task", map[string]any{"summary": "Recovered after a blip."})},
	}}
	o := newTestOrchestrator(t)
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-recover", Role: "task-runner", Status: "in_progress", Task: "build a site"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "done" {
		t.Fatalf("status = %q, want done (recovered after transient error)", rec.Status)
	}
	if rec.Result != "Recovered after a blip." {
		t.Fatalf("result = %q, want the complete_task summary", rec.Result)
	}
}

// TestTaskRunnerNarrationIsBoundedByTurnBudget documents the corrected design:
// narrating ("thinking out loud") without immediately calling a tool is NORMAL,
// healthy model behavior - it is NOT failed prematurely. A task-runner that only
// ever narrates and never signals completion is bounded by the per-role turn
// budget (maxInferenceTurns), runs that whole budget, and only then ends as a
// failure with a clear reason. Crucially it is the legitimate budget - not a
// bespoke "you narrated N times" guard - that bounds it, and the worker is never
// killed mid-flight.
func TestTaskRunnerNarrationIsBoundedByTurnBudget(t *testing.T) {
	narrate := func(s string) []bridge.StreamEvent {
		return []bridge.StreamEvent{{Kind: bridge.EventResponseDelta, Delta: s}}
	}
	// Far more narration turns than the budget below, each re-phrased so the
	// no-progress hash guard never trips - only the turn budget can stop it.
	turns := make([][]bridge.StreamEvent, 0, 12)
	phrases := []string{
		"Saya akan membuat js/script.js sekarang.",
		"Saya pikir dulu strukturnya sebelum menulis.",
		"Baik, saya buat js/script.js menggunakan create_file.",
		"Sebentar, saya rencanakan section-nya.",
		"Oke, sekarang saya tulis filenya.",
		"Saya susun dulu HTML-nya di kepala.",
	}
	for i := 0; i < 12; i++ {
		turns = append(turns, narrate(phrases[i%len(phrases)]+" #"+string(rune('a'+i))))
	}
	fake := &scriptedBridge{turns: turns}

	o := newTestOrchestrator(t)
	// Small per-role budget so the test is fast but still proves the budget
	// (not a narration counter) is what bounds the run.
	o.cfg.SubAgents = config.SubAgentsConfig{Roles: map[string]config.SubAgentRole{
		"task-runner": {MaxTurns: 4},
	}}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-narrate", Role: "task-runner", Status: "in_progress", Task: "build a site"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "failed" {
		t.Fatalf("status = %q, want failed (no terminal tool within the turn budget)", rec.Status)
	}
	if rec.Error == "" {
		t.Fatalf("a run that never completes must record a concrete failure reason")
	}
	// It must consume the whole turn budget (narration is allowed), not bail
	// out early on a narration-count heuristic.
	if fake.call != 4 {
		t.Fatalf("narration should run the full turn budget (4), got %d turns", fake.call)
	}
}

// TestSubAgentSinkBeatUpdatesPhaseNotHeartbeat documents the structural-liveness
// design: the sub-agent sink's beat() only annotates the PHASE for
// observability - it deliberately does NOT advance the heartbeat. Liveness is
// owned by the heartbeat ticker in runBackgroundTask, which runs for as long as
// the worker goroutine lives. This is the fix for the recurring false-kills: a
// long synchronous tool / slow stream no longer needs to emit events to stay
// alive, so the watchdog only catches a genuinely wedged goroutine.
func TestSubAgentSinkBeatUpdatesPhaseNotHeartbeat(t *testing.T) {
	o := newTestOrchestrator(t)
	o.workers.register("task-beat", "task-runner", "s1", "")

	before := o.workers.snapshot()
	if len(before) != 1 {
		t.Fatalf("expected 1 registered worker, got %d", len(before))
	}
	start := before[0].LastHeartbeat

	sink := &subagentSink{o: o, taskID: "task-beat"}
	time.Sleep(2 * time.Millisecond)
	sink.beat("responding turn 1/24")

	after := o.workers.snapshot()
	if len(after) != 1 {
		t.Fatalf("worker vanished: %d", len(after))
	}
	// Phase is updated...
	if after[0].Phase != "responding turn 1/24" {
		t.Fatalf("phase = %q, want the streamed phase", after[0].Phase)
	}
	// ...but the heartbeat is NOT advanced by the sink (the ticker owns it).
	if after[0].LastHeartbeat != start {
		t.Fatalf("sink.beat must not advance heartbeat: start=%v after=%v", start, after[0].LastHeartbeat)
	}
}

// TestWorkerHeartbeatAdvancesLiveness confirms the registry heartbeat (the call
// the structural ticker makes) advances LastHeartbeat so the watchdog sees the
// goroutine as alive.
func TestWorkerHeartbeatAdvancesLiveness(t *testing.T) {
	o := newTestOrchestrator(t)
	o.workers.register("task-hb", "task-runner", "s1", "")
	start := o.workers.snapshot()[0].LastHeartbeat
	time.Sleep(2 * time.Millisecond)

	o.workers.heartbeat("task-hb", "")

	after := o.workers.snapshot()[0].LastHeartbeat
	if !after.After(start) {
		t.Fatalf("registry heartbeat did not advance: start=%v after=%v", start, after)
	}
}
