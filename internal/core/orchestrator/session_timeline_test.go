package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestSessionTimelineToolCallsAndTasks(t *testing.T) {
	now := time.Now().UTC()
	progressDir := t.TempDir()
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		progress:  newAsyncProgressWriter(ProgressWriter{Dir: progressDir}),
	}
	sessionID := "chat-test"
	progressPath := filepath.Join(progressDir, "orch-"+sessionID+".jsonl")
	toolEv := bridge.NewEvent(bridge.EventToolCall)
	toolEv.SessionID = sessionID
	toolEv.ToolCall = &parse.ToolCall{Name: "sapaloq_spawn_agent"}
	toolEv.At = now
	if err := o.progress.inner.Append(sessionID, toolEv); err != nil {
		t.Fatal(err)
	}
	// Unrelated session tool call must not leak in.
	other := bridge.NewEvent(bridge.EventToolCall)
	other.SessionID = "chat-other"
	other.ToolCall = &parse.ToolCall{Name: "sapaloq_stop"}
	if err := o.progress.inner.Append("chat-other", other); err != nil {
		t.Fatal(err)
	}
	for _, record := range []taskRecord{
		{ID: "t-live", SessionID: sessionID, Role: "task-runner", Status: "in_progress", CreatedAt: now, UpdatedAt: now},
		{ID: "t-done", SessionID: sessionID, Role: "planner", Status: "done", Result: "ok", CreatedAt: now, UpdatedAt: now.Add(2 * time.Second)},
		{ID: "t-other", SessionID: "chat-other", Role: "task-runner", Status: "done", CreatedAt: now, UpdatedAt: now},
	} {
		if err := o.writeTask(record); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(progressPath); err != nil {
		t.Fatalf("progress file: %v", err)
	}

	events := o.SessionTimeline(sessionID)
	if len(events) != 3 {
		t.Fatalf("got %d timeline events, want 3 (tool + live task + terminal task): %+v", len(events), events)
	}
	if events[0].Kind != bridge.EventToolCall || events[0].ToolCall == nil || events[0].ToolCall.Name != "sapaloq_spawn_agent" {
		t.Fatalf("first event should be spawn tool call: %+v", events[0])
	}
	ids := map[string]bool{}
	for _, ev := range events[1:] {
		if ev.Kind != bridge.EventTaskUpdate {
			t.Fatalf("expected task_update, got %+v", ev)
		}
		ids[ev.TaskID] = true
	}
	if !ids["t-live"] || !ids["t-done"] {
		t.Fatalf("missing task cards: %+v", ids)
	}
}
