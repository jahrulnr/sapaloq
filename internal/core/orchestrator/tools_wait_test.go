package orchestrator

// tools_wait_test.go covers the unified `wait` tool (all 4 modes) and the
// wait_for_output:false fire-and-forget path end-to-end through the real
// dispatchAskTool surface, plus sapaloq_cancel_job.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// testSnap builds a providerSnapshot with a small wait window for the time/task
// modes (which read snap.cfg.Orchestrator.Continuation.MaxWaitSeconds).
func testSnap(maxWait int) providerSnapshot {
	return providerSnapshot{cfg: config.Config{
		Orchestrator: config.OrchestratorConfig{
			Continuation: config.ContinuationConfig{MaxWaitSeconds: maxWait},
		},
	}}
}

func bgJobIDFromText(t *testing.T, text string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("expected JSON spawn response, got %q: %v", text, err)
	}
	id, _ := m["job_id"].(string)
	if id == "" {
		t.Fatalf("spawn response missing job_id: %v", m)
	}
	return id
}

// TestWaitForOutputFalseReturnsJobID proves a work tool with
// wait_for_output:false returns immediately with {job_id, queued:true} and the
// result is later collectable via `wait mode:tool`.
func TestWaitForOutputFalseReturnsJobID(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir()}
	ctx := withActorRunID(context.Background(), "run-x")

	call := parse.ToolCall{
		Name:      "read_file",
		Arguments: []byte(`{"path":"/etc/hostname","wait_for_output":false}`),
	}
	text, ok := o.runSharedTool(ctx, call)
	if !ok {
		t.Fatal("runSharedTool returned handled=false for read_file")
	}
	if !strings.Contains(text, `"queued":true`) {
		t.Fatalf("expected queued:true JSON, got %q", text)
	}
	jobID := bgJobIDFromText(t, text)

	waitCall := parse.ToolCall{
		Name:      "wait",
		Arguments: []byte(`{"mode":"tool","job_id":"` + jobID + `","timeout_seconds":5}`),
	}
	out := make(chan bridge.StreamEvent, 8)
	res := o.dispatchAskTool(ctx, testSnap(5), out, "sess", "", waitCall, parseToolArgs(waitCall.Arguments))
	if !res.handled {
		t.Fatalf("wait tool not handled: %+v", res)
	}
	if !strings.Contains(res.text, `"status":"completed"`) {
		t.Fatalf("expected completed job via wait, got %q", res.text)
	}
}

// TestWaitToolModeToolPeekRunning: timeout_seconds:0 is an instant peek that
// returns status:"running" for an in-flight job.
func TestWaitToolModeToolPeekRunning(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir()}
	ctx := withActorRunID(context.Background(), "run-x")
	spawn := o.spawnBgTool(ctx, "exec", func(ctx context.Context) (string, error) {
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
		}
		return "late", nil
	})
	jobID := bgJobIDFromText(t, spawn)
	time.Sleep(80 * time.Millisecond)

	waitCall := parse.ToolCall{
		Name:      "wait",
		Arguments: []byte(`{"mode":"tool","job_id":"` + jobID + `","timeout_seconds":0}`),
	}
	out := make(chan bridge.StreamEvent, 8)
	res := o.dispatchAskTool(ctx, testSnap(5), out, "sess", "", waitCall, parseToolArgs(waitCall.Arguments))
	if !strings.Contains(res.text, `"status":"running"`) {
		t.Fatalf("expected running on instant peek, got %q", res.text)
	}
	o.bgJobs().cancel(jobID)
}

// TestWaitToolModeTime sleeps and returns a "Waited Ns." message.
func TestWaitToolModeTime(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir()}
	ctx := context.Background()
	waitCall := parse.ToolCall{
		Name:      "wait",
		Arguments: []byte(`{"mode":"time","seconds":1}`),
	}
	out := make(chan bridge.StreamEvent, 8)
	start := time.Now()
	res := o.dispatchAskTool(ctx, testSnap(5), out, "sess", "", waitCall, parseToolArgs(waitCall.Arguments))
	elapsed := time.Since(start)
	if !strings.Contains(res.text, "Waited") {
		t.Fatalf("expected 'Waited' message, got %q", res.text)
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("wait(time) did not actually sleep: elapsed=%s", elapsed)
	}
}

// TestSapaloqCancelJob cancels a running background job.
func TestSapaloqCancelJob(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir()}
	ctx := withActorRunID(context.Background(), "run-x")
	spawn := o.spawnBgTool(ctx, "exec", func(ctx context.Context) (string, error) {
		select {
		case <-time.After(30 * time.Second):
		case <-ctx.Done():
		}
		return "", ctx.Err()
	})
	jobID := bgJobIDFromText(t, spawn)
	time.Sleep(80 * time.Millisecond)

	cancelCall := parse.ToolCall{
		Name:      "sapaloq_cancel_job",
		Arguments: []byte(`{"job_id":"` + jobID + `"}`),
	}
	out := make(chan bridge.StreamEvent, 8)
	res := o.dispatchAskTool(ctx, testSnap(5), out, "sess", "", cancelCall, parseToolArgs(cancelCall.Arguments))
	if !strings.Contains(res.text, `"status":"cancelled"`) {
		t.Fatalf("expected cancelled, got %q", res.text)
	}
}

// TestWaitToolModeToolMissingJob: an unknown job_id is handled without panic.
func TestWaitToolModeToolMissingJob(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir()}
	ctx := context.Background()
	waitCall := parse.ToolCall{
		Name:      "wait",
		Arguments: []byte(`{"mode":"tool","job_id":"bg-nope","timeout_seconds":0}`),
	}
	out := make(chan bridge.StreamEvent, 8)
	res := o.dispatchAskTool(ctx, testSnap(5), out, "sess", "", waitCall, parseToolArgs(waitCall.Arguments))
	if !res.handled {
		t.Fatalf("wait tool not handled: %+v", res)
	}
}

// TestWaitToolModeEventsNoEvents returns the "no actor event" message when
// nothing arrives in the window.
func TestWaitToolModeEventsNoEvents(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir()}
	ctx := context.Background()
	waitCall := parse.ToolCall{
		Name:      "wait",
		Arguments: []byte(`{"mode":"events","timeout_seconds":1}`),
	}
	out := make(chan bridge.StreamEvent, 8)
	res := o.dispatchAskTool(ctx, testSnap(5), out, "sess", "", waitCall, parseToolArgs(waitCall.Arguments))
	if !strings.Contains(res.text, "No actor event") {
		t.Fatalf("expected no-actor-event message, got %q", res.text)
	}
}

// TestWaitToolRequiresJobIDForToolMode: mode=tool without job_id errors clearly.
func TestWaitToolRequiresJobIDForToolMode(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir()}
	ctx := context.Background()
	waitCall := parse.ToolCall{
		Name:      "wait",
		Arguments: []byte(`{"mode":"tool"}`),
	}
	out := make(chan bridge.StreamEvent, 8)
	res := o.dispatchAskTool(ctx, testSnap(5), out, "sess", "", waitCall, parseToolArgs(waitCall.Arguments))
	if !strings.Contains(res.text, "job_id is required") {
		t.Fatalf("expected job_id-required error, got %q", res.text)
	}
}

// TestWaitToolUnknownModeErrors: an unknown mode surfaces a clear error.
func TestWaitToolUnknownModeErrors(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir()}
	ctx := context.Background()
	waitCall := parse.ToolCall{
		Name:      "wait",
		Arguments: []byte(`{"mode":"bogus"}`),
	}
	out := make(chan bridge.StreamEvent, 8)
	res := o.dispatchAskTool(ctx, testSnap(5), out, "sess", "", waitCall, parseToolArgs(waitCall.Arguments))
	if !strings.Contains(res.text, "unknown wait mode") {
		t.Fatalf("expected unknown-wait-mode error, got %q", res.text)
	}
}
