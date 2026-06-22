package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// TestWatchdogFailsStalledWorker proves a wedged worker (no heartbeat) is
// force-failed with an explicit reason instead of sitting at "in_progress"
// forever — the core of the "we never know if the agent finished" bug.
func TestWatchdogFailsStalledWorker(t *testing.T) {
	dir := t.TempDir()
	b := bus.New()
	events, cancel := b.Subscribe(8)
	defer cancel()
	o := &Orchestrator{
		memoryDir:  dir,
		workersDir: filepath.Join(dir, "workers"),
		workers:    newWorkerRegistry(filepath.Join(dir, "workers")),
		bus:        b,
		progress:   ProgressWriter{Dir: t.TempDir()},
		cfg: config.Config{Orchestrator: config.OrchestratorConfig{
			Completion: config.CompletionConfig{WorkerErrorLog: true},
		}},
	}

	// A registered worker with a persisted in_progress record but no recent
	// heartbeat (we backdate it).
	rec := taskRecord{ID: "task-stall-1", SessionID: "s1", Role: "task-runner", Status: "in_progress", UpdatedAt: time.Now().UTC()}
	if err := o.writeTask(rec); err != nil {
		t.Fatal(err)
	}
	o.workers.register(rec.ID, rec.Role, rec.SessionID, "")
	// Force the heartbeat into the past.
	o.workers.mu.Lock()
	o.workers.workers[rec.ID].LastHeartbeat = time.Now().UTC().Add(-10 * time.Minute)
	o.workers.mu.Unlock()

	o.sweepStalledWorkers(1 * time.Minute)

	got, err := o.readTask(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" {
		t.Fatalf("stalled worker status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "stalled") {
		t.Fatalf("error = %q, want a stall reason", got.Error)
	}

	// An error-log line must have been written for debugging.
	logPath := filepath.Join(dir, "workers", rec.ID, "error.log")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected per-worker error log at %s: %v", logPath, err)
	}
	if !strings.Contains(string(raw), "stalled") {
		t.Fatalf("error log missing stall reason: %s", raw)
	}

	// And a failed task_update must have been published.
	sawFailed := false
	for drained := false; !drained; {
		select {
		case ev := <-events:
			if ev.Data.Kind == bridge.EventTaskUpdate && ev.Data.TaskStatus == "failed" {
				sawFailed = true
			}
		default:
			drained = true
		}
	}
	if !sawFailed {
		t.Fatalf("watchdog did not publish a failed task_update")
	}
}

// TestWatchdogLeavesHealthyWorkerAlone confirms a worker that recently
// heartbeat is not touched by the sweep.
func TestWatchdogLeavesHealthyWorkerAlone(t *testing.T) {
	dir := t.TempDir()
	o := &Orchestrator{
		memoryDir:  dir,
		workersDir: filepath.Join(dir, "workers"),
		workers:    newWorkerRegistry(filepath.Join(dir, "workers")),
		progress:   ProgressWriter{Dir: t.TempDir()},
		cfg:        config.Config{},
	}
	rec := taskRecord{ID: "task-live-1", SessionID: "s1", Role: "task-runner", Status: "in_progress", UpdatedAt: time.Now().UTC()}
	if err := o.writeTask(rec); err != nil {
		t.Fatal(err)
	}
	o.workers.register(rec.ID, rec.Role, rec.SessionID, "")
	o.workers.heartbeat(rec.ID, "working")

	o.sweepStalledWorkers(1 * time.Minute)

	got, err := o.readTask(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Fatalf("healthy worker status = %q, want in_progress (untouched)", got.Status)
	}
}

// TestWorkerHealthSnapshotPersisted verifies the registry writes an observable
// health.json per worker so liveness can be inspected from outside the process.
func TestWorkerHealthSnapshotPersisted(t *testing.T) {
	dir := t.TempDir()
	reg := newWorkerRegistry(dir)
	reg.register("task-h-1", "planner", "s1", "local-default")
	reg.heartbeat("task-h-1", "inference turn 1/12")

	raw, err := os.ReadFile(filepath.Join(dir, "task-h-1", "health.json"))
	if err != nil {
		t.Fatalf("expected health snapshot: %v", err)
	}
	for _, want := range []string{"task-h-1", "planner", "\"pid\"", "inference turn 1/12"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("health.json missing %q: %s", want, raw)
		}
	}
	_ = context.Background()
}
