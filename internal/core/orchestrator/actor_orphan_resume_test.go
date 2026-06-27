package orchestrator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func orphanRecoveryFixture(t *testing.T) (*Orchestrator, *chatstore.Store) {
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

func TestRecoverOrphanedTasksResumesWhenTurnsExist(t *testing.T) {
	o, store := orphanRecoveryFixture(t)
	taskID := "task-resume"
	ctx := context.Background()
	if err := store.AppendTurn(ctx, taskID, "user", "build landing page", 4); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(ctx, taskID, "assistant", "starting work", 3); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := taskRecord{
		ID: taskID, SessionID: "chat-1", Role: "task-runner",
		Status: "in_progress", Task: "build landing page",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := o.writeTask(rec); err != nil {
		t.Fatal(err)
	}

	o.recoverOrphanedTasks()
	got, err := o.readTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" && got.Status != "done" {
		t.Fatalf("status after recovery = %q, want pending or done after resume", got.Status)
	}
	// Allow the background resume goroutine to finish without racing test teardown.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ = o.readTask(taskID)
		if got.Status == "done" || got.Status == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRecoverOrphanedTasksFailsWithoutTurns(t *testing.T) {
	o, _ := orphanRecoveryFixture(t)
	taskID := "task-orphan"
	now := time.Now().UTC()
	rec := taskRecord{
		ID: taskID, SessionID: "chat-1", Role: "task-runner",
		Status: "in_progress", Task: "build",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := o.writeTask(rec); err != nil {
		t.Fatal(err)
	}
	o.recoverOrphanedTasks()
	got, err := o.readTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
}
