package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// writeInspectFixture stands up a task dir + progress file + optional plan.md
// under a temp memory dir and returns an Orchestrator wired to it.
func writeInspectFixture(t *testing.T, record taskRecord, progressLines []bridge.StreamEvent, plan string) *Orchestrator {
	t.Helper()
	memDir := t.TempDir()
	progressDir := filepath.Join(memDir, "progress")
	if err := os.MkdirAll(progressDir, 0o700); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{
		memoryDir: memDir,
		tasksDir:  filepath.Join(memDir, "tasks"),
		progress:  newAsyncProgressWriter(ProgressWriter{Dir: progressDir}),
	}
	if err := o.writeTask(record); err != nil {
		t.Fatal(err)
	}
	if plan != "" {
		if err := os.WriteFile(filepath.Join(o.taskDir(record.ID), "plan.md"), []byte(plan), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if len(progressLines) > 0 {
		f, err := os.Create(filepath.Join(progressDir, "orch-"+record.ID+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		enc := json.NewEncoder(f)
		for _, ev := range progressLines {
			if err := enc.Encode(ev); err != nil {
				f.Close()
				t.Fatal(err)
			}
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func TestTaskInspectReadsRecordProgressAndPlan(t *testing.T) {
	rec := taskRecord{
		ID: "task-ok", Role: "planner", Status: "done",
		Task: "plan the migration", Result: "shipped", UpdatedAt: time.Now().UTC(),
	}
	events := []bridge.StreamEvent{
		{Kind: bridge.EventThinkingDelta, Delta: "let me think"},
		{Kind: bridge.EventResponseDelta, Delta: "Hello "},
		{Kind: bridge.EventResponseDelta, Delta: "world"},
	}
	o := writeInspectFixture(t, rec, events, "# Plan\n1. step one")

	got, err := o.TaskInspect("task-ok", 0)
	if err != nil {
		t.Fatalf("TaskInspect: %v", err)
	}
	if got.Role != "planner" || got.Status != "done" || got.Task != "plan the migration" {
		t.Fatalf("record projection mismatch: %+v", got)
	}
	if got.Plan == "" || !strings.Contains(got.Plan, "step one") {
		t.Fatalf("plan markdown missing/got=%q", got.Plan)
	}
	if len(got.Events) != 3 {
		t.Fatalf("event count = %d, want 3", len(got.Events))
	}
	if got.EventCount != 3 {
		t.Fatalf("event_count = %d, want 3", got.EventCount)
	}
	if got.Events[1].Delta != "Hello " || got.Events[2].Delta != "world" {
		t.Fatalf("event order/delta mismatch: %q %q", got.Events[1].Delta, got.Events[2].Delta)
	}
}

func TestTaskInspectAfterLineIncremental(t *testing.T) {
	rec := taskRecord{ID: "task-inc", Role: "task-runner", Status: "in_progress", Task: "build", UpdatedAt: time.Now().UTC()}
	events := []bridge.StreamEvent{
		{Kind: bridge.EventResponseDelta, Delta: "a"},
		{Kind: bridge.EventResponseDelta, Delta: "b"},
		{Kind: bridge.EventResponseDelta, Delta: "c"},
		{Kind: bridge.EventResponseDelta, Delta: "d"},
	}
	o := writeInspectFixture(t, rec, events, "")

	first, err := o.TaskInspect("task-inc", 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.EventCount != 4 || len(first.Events) != 4 {
		t.Fatalf("first fetch: count=%d events=%d", first.EventCount, len(first.Events))
	}
	// Incremental: only events after line 2.
	second, err := o.TaskInspect("task-inc", 2)
	if err != nil {
		t.Fatal(err)
	}
	if second.EventCount != 4 {
		t.Fatalf("event_count should stay total, got %d", second.EventCount)
	}
	if len(second.Events) != 2 {
		t.Fatalf("incremental events = %d, want 2", len(second.Events))
	}
	if second.Events[0].Delta != "c" || second.Events[1].Delta != "d" {
		t.Fatalf("incremental order mismatch: %q %q", second.Events[0].Delta, second.Events[1].Delta)
	}
}

func TestTaskInspectInvalidID(t *testing.T) {
	o := &Orchestrator{memoryDir: t.TempDir(), progress: newAsyncProgressWriter(ProgressWriter{Dir: t.TempDir()})}
	for _, bad := range []string{"", "..", "a/b", "../x"} {
		if _, err := o.TaskInspect(bad, 0); err == nil {
			t.Fatalf("expected error for id %q", bad)
		}
	}
}

func TestTaskInspectUnknownTask(t *testing.T) {
	o := &Orchestrator{memoryDir: t.TempDir(), tasksDir: filepath.Join(t.TempDir(), "tasks"), progress: newAsyncProgressWriter(ProgressWriter{Dir: t.TempDir()})}
	if _, err := o.TaskInspect("nope", 0); err == nil {
		t.Fatalf("expected error for unknown task id")
	}
}

func TestTaskInspectAgentPlanViaPlanTaskID(t *testing.T) {
	agent := taskRecord{ID: "task-agent", Role: "task-runner", Status: "in_progress", Task: "execute", PlanTaskID: "task-plan", UpdatedAt: time.Now().UTC()}
	o := writeInspectFixture(t, agent, nil, "")
	// The handed-off plan lives in the planner's task dir, not the agent's.
	planDir := o.taskDir("task-plan")
	if err := os.MkdirAll(planDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "plan.md"), []byte("# Handed-off\n- do X"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := o.TaskInspect("task-agent", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Plan, "Handed-off") {
		t.Fatalf("agent should expose handed-off plan via PlanTaskID, got=%q", got.Plan)
	}
}

func TestTaskInspectMissingProgressReturnsRecord(t *testing.T) {
	rec := taskRecord{ID: "task-noprogress", Role: "planner", Status: "pending", Task: "queued", UpdatedAt: time.Now().UTC()}
	o := writeInspectFixture(t, rec, nil, "")
	got, err := o.TaskInspect("task-noprogress", 0)
	if err != nil {
		t.Fatalf("missing progress should not fail the whole inspect: %v", err)
	}
	if got.Status != "pending" {
		t.Fatalf("status=%q want pending", got.Status)
	}
	if len(got.Events) != 0 {
		t.Fatalf("events should be empty, got %d", len(got.Events))
	}
}
