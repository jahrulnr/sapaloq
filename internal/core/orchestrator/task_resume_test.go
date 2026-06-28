package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func resumeTestFixture(t *testing.T) (*Orchestrator, *chatstore.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := chatstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		{toolCallEvent("sapaloq_complete_task", map[string]any{"summary": "resumed ok"})},
	}}
	o := &Orchestrator{
		memoryDir:  dir,
		stateDir:   filepath.Join(dir, "state"),
		tasksDir:   filepath.Join(dir, "state", "tasks"),
		workersDir: filepath.Join(dir, "workers"),
		workers:    newWorkerRegistry(filepath.Join(dir, "workers")),
		progress:   newAsyncProgressWriter(ProgressWriter{Dir: filepath.Join(dir, "rollout")}),
		chat:       store,
		cfg:        config.Config{},
		bridge:     fake,
		entry:      config.LLMBridge{Key: "k", Model: "m"},
	}
	return o, store
}

func TestResumeTaskContinuesFailedTaskWithTurns(t *testing.T) {
	o, store := resumeTestFixture(t)
	taskID := "task-failed-resume"
	sessionID := "chat-resume"
	ctx := context.Background()
	if err := store.AppendTurn(ctx, taskID, "user", "build feature", 4); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(ctx, taskID, "assistant", "explored codebase", 3); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := taskRecord{
		ID: taskID, SessionID: sessionID, Role: "task-runner",
		Status: "failed", Task: "build feature",
		Error: "provider connection reset",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := o.writeTask(rec); err != nil {
		t.Fatal(err)
	}

	call := parse.ToolCall{Name: "sapaloq_resume_task", Arguments: []byte(`{"task_id":"` + taskID + `"}`)}
	res := o.dispatchAskTool(ctx, providerSnapshot{}, nil, sessionID, "", call, parseToolArgs(call.Arguments))
	if !res.handled || strings.Contains(res.text, "Cannot resume") {
		t.Fatalf("resume dispatch = %+v", res)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := o.readTask(taskID)
		if got.Status == "done" || got.Status == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, err := o.readTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("status after resume = %q, want done", got.Status)
	}
}

func TestResumeTaskRejectsWithoutTurns(t *testing.T) {
	o, _ := resumeTestFixture(t)
	taskID := "task-no-turns"
	now := time.Now().UTC()
	rec := taskRecord{
		ID: taskID, SessionID: "chat-1", Role: "task-runner",
		Status: "failed", Task: "build", Error: "provider timeout",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := o.writeTask(rec); err != nil {
		t.Fatal(err)
	}
	call := parse.ToolCall{Name: "sapaloq_resume_task", Arguments: []byte(`{"task_id":"` + taskID + `"}`)}
	res := o.dispatchAskTool(context.Background(), providerSnapshot{}, nil, "chat-1", "", call, parseToolArgs(call.Arguments))
	if !strings.Contains(res.text, "no persisted turns") {
		t.Fatalf("expected no-turns error, got %q", res.text)
	}
}

func TestSpawnAllowedWhenResumableTaskExists(t *testing.T) {
	o, store := resumeTestFixture(t)
	taskID := "task-block-spawn"
	ctx := context.Background()
	if err := store.AppendTurn(ctx, taskID, "assistant", "partial work", 3); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := taskRecord{
		ID: taskID, SessionID: "chat-1", Role: "task-runner",
		Status: "failed", Task: "build", Error: "connection reset",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := o.writeTask(rec); err != nil {
		t.Fatal(err)
	}
	call := parse.ToolCall{Name: "sapaloq_spawn_agent", Arguments: []byte(`{"task":"explore other repo"}`)}
	res := o.dispatchAskTool(ctx, providerSnapshot{}, nil, "chat-1", "", call, parseToolArgs(call.Arguments))
	if !strings.Contains(res.text, "Agent started in background") {
		t.Fatalf("expected parallel spawn to succeed, got %q", res.text)
	}
	// Allow background spawn goroutine to settle before TempDir cleanup.
	time.Sleep(50 * time.Millisecond)
}

func TestDeleteSessionPurgesTaskArtifacts(t *testing.T) {
	o, store := resumeTestFixture(t)
	sessionID := "chat-delete"
	taskID := "task-purge"
	ctx := context.Background()
	if err := store.AppendTurn(ctx, taskID, "assistant", "work", 2); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := taskRecord{
		ID: taskID, SessionID: sessionID, Role: "task-runner",
		Status: "failed", Task: "build", Error: "timeout",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := o.writeTask(rec); err != nil {
		t.Fatal(err)
	}
	wsPath := o.workspaceStatePath(taskID)
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wsPath, []byte(`{"cwd":"/tmp"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	inbox := o.actorInboxRoot(taskID)
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "event-1.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	o.purgeSessionTasks(sessionID)

	if _, err := os.Stat(o.taskDir(taskID)); !os.IsNotExist(err) {
		t.Fatalf("task dir still exists: %v", err)
	}
	if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists: %v", err)
	}
	if _, err := os.Stat(inbox); !os.IsNotExist(err) {
		t.Fatalf("inbox still exists: %v", err)
	}
}
