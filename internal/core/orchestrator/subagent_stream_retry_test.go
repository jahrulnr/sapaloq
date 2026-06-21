package orchestrator

import (
	"context"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// TestTaskRunnerRetriesTruncatedStream proves the SSE-hang / truncation fix:
// a turn that emits partial text and then an EventError (the upstream went
// silent mid-response — the exact "You're right, let me actually" failure) must
// be RE-ISSUED, not treated as the model idling or as an immediate failure. The
// task then completes normally on the retry.
func TestTaskRunnerRetriesTruncatedStream(t *testing.T) {
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		// Turn 1: partial text, then the stream dies (idle/SSE fault).
		{
			{Kind: bridge.EventResponseDelta, Delta: "You're right, let me actually"},
			{Kind: bridge.EventError, Error: "provider-bridge: SSE idle timeout: no data from upstream"},
		},
		// Turn 2 (the retry): it actually finishes.
		{toolCallEvent("sapaloq_complete_task", map[string]any{"summary": "Built after retry."})},
	}}
	o := &Orchestrator{memoryDir: t.TempDir(), cfg: config.Config{}}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-retry", Role: "task-runner", Status: "in_progress", Task: "build a site"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "done" {
		t.Fatalf("status = %q, want done (recovered via retry, not failed on transient stream fault)", rec.Status)
	}
	if rec.Result != "Built after retry." {
		t.Fatalf("result = %q, want the complete_task summary", rec.Result)
	}
	if fake.call < 2 {
		t.Fatalf("expected the truncated turn to be retried (>=2 Complete calls), got %d", fake.call)
	}
}

// TestTaskRunnerRetriesEmptyStream covers the silent-truncation variant: the
// stream connects and closes producing no text and no tool call. That is a
// transport fault, not the model narrating intent, so it must be retried rather
// than counting against the idle-nudge budget.
func TestTaskRunnerRetriesEmptyStream(t *testing.T) {
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		// Turn 1: completely empty (only the auto-appended EventDone).
		{},
		// Turn 2 (retry): finishes.
		{toolCallEvent("sapaloq_complete_task", map[string]any{"summary": "Recovered."})},
	}}
	o := &Orchestrator{memoryDir: t.TempDir(), cfg: config.Config{}}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-empty", Role: "task-runner", Status: "in_progress", Task: "do it"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "done" {
		t.Fatalf("status = %q, want done", rec.Status)
	}
	if fake.call < 2 {
		t.Fatalf("empty stream should be retried, got %d Complete calls", fake.call)
	}
}

// TestTaskRunnerFailsWhenStreamPersistentlyBroken confirms the retry budget is
// bounded: a stream that errors on every turn eventually fails the task with a
// concrete error instead of looping forever.
func TestTaskRunnerFailsWhenStreamPersistentlyBroken(t *testing.T) {
	errTurn := []bridge.StreamEvent{
		{Kind: bridge.EventError, Error: "provider-bridge: SSE idle timeout: no data from upstream"},
	}
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{errTurn, errTurn, errTurn, errTurn, errTurn}}
	o := &Orchestrator{memoryDir: t.TempDir(), cfg: config.Config{}}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-broken", Role: "task-runner", Status: "in_progress", Task: "do it"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "failed" {
		t.Fatalf("status = %q, want failed after exhausting retries", rec.Status)
	}
	if rec.Error == "" {
		t.Fatalf("a persistently broken stream must record a concrete error")
	}
}
