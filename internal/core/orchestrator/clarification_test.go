package orchestrator

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// countingBridge records how many Complete() calls it received, so a test can
// assert whether the orchestrator's clarification resolver actually ran an
// inference (auto-answer attempt) or was correctly gated/skipped. It always
// finishes a turn with no tool call (a "not confident" orchestrator), so the
// resume path is never triggered and the test stays deterministic.
//
// calls is accessed from both the async resolver goroutine (via Complete) and
// the test goroutine (via waitForCalls), so it is atomic to stay race-clean.
type countingBridge struct{ calls int64 }

func (b *countingBridge) callCount() int { return int(atomic.LoadInt64(&b.calls)) }

func (b *countingBridge) ID() string              { return "counting" }
func (b *countingBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *countingBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	atomic.AddInt64(&b.calls, 1)
	out := make(chan bridge.StreamEvent, 2)
	go func() {
		defer close(out)
		out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "I am not sure; the user should decide."}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

func newClarifyOrchestrator(t *testing.T, br bridge.Bridge) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	return &Orchestrator{
		memoryDir: dir,
		cfg:       config.Config{},
		bridge:    br,
		entry:     config.LLMBridge{Key: "k", Model: "m"},
		workers:   newWorkerRegistry(filepath.Join(dir, "workers")),
		progress:  ProgressWriter{Dir: filepath.Join(dir, "progress")},
	}
}

// waitForCalls polls until the async resolver has invoked the bridge at least n
// times, or fails after a short timeout.
func waitForCalls(t *testing.T, br *countingBridge, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if br.callCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("resolver did not run: bridge calls = %d, want >= %d", br.callCount(), n)
}

// TestClarificationResolverRunsThenDefersToUser proves the event-driven
// clarification loop: the orchestrator reuses the chat engine to TRY to answer a
// sub-agent's question itself. Here it is "not confident" (no tool call), so it
// correctly defers to the user — the task is NOT resumed, and nothing blocks.
func TestClarificationResolverRunsThenDefersToUser(t *testing.T) {
	br := &countingBridge{}
	o := newClarifyOrchestrator(t, br)
	rec := taskRecord{ID: "task-clarify", Role: "task-runner", Status: "awaiting_clarification", Task: "build a site", Question: "Dark or light theme?"}

	o.resolveClarification("s1", rec)
	waitForCalls(t, br, 1)

	// The resolver consulted the model exactly once for this attempt.
	if br.callCount() != 1 {
		t.Fatalf("bridge calls = %d, want exactly 1", br.callCount())
	}
}

// TestClarificationAutoAnswerBudget proves the orchestrator stops auto-answering
// a single task after maxAutoClarifyAnswers attempts and escalates to the user,
// preventing an auto-answer ↔ re-ask ping-pong loop.
func TestClarificationAutoAnswerBudget(t *testing.T) {
	br := &countingBridge{}
	o := newClarifyOrchestrator(t, br)
	rec := taskRecord{ID: "task-budget", Role: "task-runner", Status: "awaiting_clarification", Task: "do it", Question: "Which one?"}

	// First maxAutoClarifyAnswers calls each trigger a resolver run.
	for i := 0; i < maxAutoClarifyAnswers; i++ {
		o.resolveClarification("s1", rec)
	}
	waitForCalls(t, br, maxAutoClarifyAnswers)

	// The next call must be gated (budget spent) — no additional inference.
	o.resolveClarification("s1", rec)
	time.Sleep(50 * time.Millisecond)
	if br.callCount() != maxAutoClarifyAnswers {
		t.Fatalf("auto-answer budget not enforced: bridge calls = %d, want %d", br.callCount(), maxAutoClarifyAnswers)
	}
}

// TestClarificationResolverIgnoresNonAwaiting confirms the resolver is a no-op
// for a task that is not actually awaiting a decision (defensive guard).
func TestClarificationResolverIgnoresNonAwaiting(t *testing.T) {
	br := &countingBridge{}
	o := newClarifyOrchestrator(t, br)
	rec := taskRecord{ID: "task-done", Role: "task-runner", Status: "done", Task: "x", Question: ""}

	o.resolveClarification("s1", rec)
	time.Sleep(50 * time.Millisecond)
	if br.callCount() != 0 {
		t.Fatalf("resolver ran for a non-awaiting task: bridge calls = %d, want 0", br.callCount())
	}
}
