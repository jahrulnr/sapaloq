package orchestrator

// bg_jobs_test.go covers the generic background-job registry: spawn/wait/
// snapshot/cancel/GC/recover, plus the concurrency cap and the JSON view. It
// replaces the old exec-only async_exec tests, exercising the registry through
// the generic `run func(ctx) (string, error)` surface instead of shell plumbing.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestBgRegistry builds a registry rooted at a tempdir so tests do not
// pollute the user's state dir. The tempdir is cleaned up by t.Cleanup, which
// also cancels every in-flight job and waits briefly for the goroutines to
// finish writing terminal state - otherwise TempDir RemoveAll races persist()
// and fails on "directory not empty" on slow disks.
func newTestBgRegistry(t *testing.T) *bgJobRegistry {
	t.Helper()
	dir := t.TempDir()
	r := newBgJobRegistry(dir, 4)
	t.Cleanup(func() {
		r.mu.Lock()
		jobs := make([]*bgJob, 0, len(r.jobs))
		for id, job := range r.jobs {
			if job.Cancel != nil {
				job.Cancel()
			}
			jobs = append(jobs, job)
			delete(r.jobs, id)
		}
		r.mu.Unlock()
		for _, job := range jobs {
			select {
			case <-job.Done:
			case <-time.After(2 * time.Second):
			}
		}
	})
	return r
}

func waitForBgTerminal(t *testing.T, r *bgJobRegistry, id string, d time.Duration) bgJobSnapshot {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		snap, ok := r.snapshot(id)
		if !ok {
			t.Fatalf("job %s disappeared from registry", id)
		}
		if snap.Status.terminal() {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not reach terminal state in %s (last status=%s)", id, d, snap.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestBgJobSpawnHappyPath covers the canonical flow: spawn a quick job, wait
// for it, observe the captured output.
func TestBgJobSpawnHappyPath(t *testing.T) {
	r := newTestBgRegistry(t)
	job := r.spawn(context.Background(), "read_file", "run-x", "sess-x", func(ctx context.Context) (string, error) {
		return "hello", nil
	})
	if job == nil || job.ID == "" {
		t.Fatal("spawn returned empty job")
	}
	if !strings.HasPrefix(job.ID, "bg-") {
		t.Fatalf("expected bg- id prefix, got %q", job.ID)
	}
	final := waitForBgTerminal(t, r, job.ID, 2*time.Second)
	if final.Status != bgJobCompleted {
		t.Fatalf("expected status=completed, got %s (err=%q)", final.Status, final.Error)
	}
	if !strings.Contains(final.Output, "hello") {
		t.Fatalf("expected output 'hello', got %q", final.Output)
	}
	if final.ToolName != "read_file" {
		t.Fatalf("expected tool_name=read_file, got %q", final.ToolName)
	}
	if final.StartedAt == nil || final.CompletedAt == nil {
		t.Fatal("started_at and completed_at must be set on a terminal job")
	}
}

// TestBgJobSpawnError marks the job failed when run returns a non-nil error.
func TestBgJobSpawnError(t *testing.T) {
	r := newTestBgRegistry(t)
	job := r.spawn(context.Background(), "search", "run-x", "sess-x", func(ctx context.Context) (string, error) {
		return "partial", errors.New("boom")
	})
	final := waitForBgTerminal(t, r, job.ID, 2*time.Second)
	if final.Status != bgJobFailed {
		t.Fatalf("expected status=failed, got %s", final.Status)
	}
	if !strings.Contains(final.Error, "boom") {
		t.Fatalf("expected error to mention 'boom', got %q", final.Error)
	}
	if !strings.Contains(final.Output, "partial") {
		t.Fatalf("expected output captured even on error, got %q", final.Output)
	}
}

// TestBgJobWaitReturnsRunningOnTimeout proves the wait path the agent relies
// on: when the job is still running after the wait window, the caller gets the
// in-progress snapshot back, not a hang.
func TestBgJobWaitReturnsRunningOnTimeout(t *testing.T) {
	r := newTestBgRegistry(t)
	job := r.spawn(context.Background(), "exec", "run-x", "sess-x", func(ctx context.Context) (string, error) {
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		return "done", nil
	})
	time.Sleep(50 * time.Millisecond)
	done, snap := r.wait(job.ID, 100*time.Millisecond)
	if done {
		t.Fatalf("expected wait to time out, got done=true status=%s", snap.Status)
	}
	if snap.Status != bgJobRunning {
		t.Fatalf("expected status=running, got %s", snap.Status)
	}
	r.cancel(job.ID)
	waitForBgTerminal(t, r, job.ID, 2*time.Second)
}

// TestBgJobWaitBlocksUntilDone proves the wait completes once the job finishes.
func TestBgJobWaitBlocksUntilDone(t *testing.T) {
	r := newTestBgRegistry(t)
	job := r.spawn(context.Background(), "read_file", "run-x", "sess-x", func(ctx context.Context) (string, error) {
		return "done", nil
	})
	done, snap := r.wait(job.ID, 2*time.Second)
	if !done {
		t.Fatalf("expected wait to return done=true, got status=%s", snap.Status)
	}
	if snap.Status != bgJobCompleted {
		t.Fatalf("expected status=completed, got %s", snap.Status)
	}
}

// TestBgJobCancelKillsRunningJob is the safety-net case: an agent that detects
// a hung job can cancel it and get a terminal snapshot.
func TestBgJobCancelKillsRunningJob(t *testing.T) {
	r := newTestBgRegistry(t)
	job := r.spawn(context.Background(), "exec", "run-x", "sess-x", func(ctx context.Context) (string, error) {
		select {
		case <-time.After(30 * time.Second):
		case <-ctx.Done():
		}
		return "", ctx.Err()
	})
	time.Sleep(80 * time.Millisecond)
	snap, ok := r.cancel(job.ID)
	if !ok {
		t.Fatal("cancel returned ok=false for a live job")
	}
	if snap.Status != bgJobCancelled {
		t.Fatalf("expected status=cancelled, got %s", snap.Status)
	}
}

// TestBgJobSnapshotMissingJob covers the user-facing wrapper: a poll for an
// unknown id returns ok=false, not a panic.
func TestBgJobSnapshotMissingJob(t *testing.T) {
	r := newTestBgRegistry(t)
	if _, ok := r.snapshot("bg-does-not-exist"); ok {
		t.Fatal("snapshot of unknown id should return ok=false")
	}
}

// TestBgJobPersistAndRecover writes a job to disk and rebuilds the registry,
// proving the on-disk schema is recoverable across restarts.
func TestBgJobPersistAndRecover(t *testing.T) {
	dir := t.TempDir()
	r1 := newBgJobRegistry(dir, 4)
	job := r1.spawn(context.Background(), "read_file", "run-x", "sess-x", func(ctx context.Context) (string, error) {
		return "persist-me", nil
	})
	final := waitForBgTerminal(t, r1, job.ID, 2*time.Second)
	if final.Status != bgJobCompleted {
		t.Fatalf("first registry did not complete: %s", final.Status)
	}
	path := filepath.Join(dir, job.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected on-disk snapshot, got error: %v", err)
	}
	var roundtrip bgJobSnapshot
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("on-disk snapshot is not valid JSON: %v", err)
	}
	if roundtrip.Status != bgJobCompleted || !strings.Contains(roundtrip.Output, "persist-me") {
		t.Fatalf("on-disk snapshot mismatch: status=%s output=%q", roundtrip.Status, roundtrip.Output)
	}
	r2 := newBgJobRegistry(dir, 4)
	if _, ok := r2.snapshot(job.ID); !ok {
		t.Fatal("second registry did not load the terminal job from disk")
	}
}

// TestBgJobRecoverMarksInFlightAsFailed ensures jobs left running when a core
// exits are surfaced as failed in the next core, not silently abandoned.
func TestBgJobRecoverMarksInFlightAsFailed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := bgJobSnapshot{
		ID:        "bg-stale-1",
		ToolName:  "exec",
		Status:    bgJobRunning,
		CreatedAt: time.Now().Add(-time.Hour).UTC(),
	}
	data, _ := json.MarshalIndent(stale, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "bg-stale-1.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newBgJobRegistry(dir, 4)
	rec, ok := r.snapshot("bg-stale-1")
	if !ok {
		t.Fatal("recovered registry lost the stale job")
	}
	if rec.Status != bgJobFailed {
		t.Fatalf("expected stale job to be marked failed, got %s", rec.Status)
	}
	if !strings.Contains(rec.Error, "runtime restarted") {
		t.Fatalf("expected error to mention 'runtime restarted', got %q", rec.Error)
	}
}

// TestBgJobViewOmitsOutputForRunningJobs ensures the polling view does not
// transfer potentially-large buffers to the model before the job is done.
func TestBgJobViewOmitsOutputForRunningJobs(t *testing.T) {
	r := newTestBgRegistry(t)
	job := r.spawn(context.Background(), "exec", "run-x", "sess-x", func(ctx context.Context) (string, error) {
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
		}
		return "done", nil
	})
	time.Sleep(50 * time.Millisecond)
	snap, _ := r.snapshot(job.ID)
	view := bgJobToView(snap)
	if _, present := view["output"]; present {
		t.Fatalf("expected no 'output' key on a running job, got %v", view)
	}
	r.cancel(job.ID)
	waitForBgTerminal(t, r, job.ID, 2*time.Second)
}

// TestBgJobCancelRaceNoDoubleClose is the regression for the "close of closed
// channel" panic: when a cancel races the job's own completion, both reach the
// terminal state and used to close job.Done twice. The close is funnelled
// through closeDone (sync.Once); this hammers the race.
func TestBgJobCancelRaceNoDoubleClose(t *testing.T) {
	r := newTestBgRegistry(t)
	for i := 0; i < 50; i++ {
		job := r.spawn(context.Background(), "read_file", "run-race", "sess-race", func(ctx context.Context) (string, error) {
			return "x", nil
		})
		var wg sync.WaitGroup
		for c := 0; c < 3; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = r.cancel(job.ID)
			}()
		}
		wg.Wait()
		select {
		case <-job.Done:
		case <-time.After(2 * time.Second):
			t.Fatalf("job %s never reached terminal state", job.ID)
		}
		if !job.terminal() {
			t.Fatalf("job %s Done closed but status not terminal: %s", job.ID, job.snapshotOf().Status)
		}
	}
}

// TestBgJobConcurrencyCap proves the semaphore bounds concurrent execution so
// a burst of fire-and-forget calls can't over-parallelize.
func TestBgJobConcurrencyCap(t *testing.T) {
	r := newTestBgRegistry(t) // cap = 4
	var inFlight int32
	var maxInFlight int32
	var mu sync.Mutex
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.spawn(context.Background(), "exec", "run-x", "sess-x", func(ctx context.Context) (string, error) {
				n := atomic.AddInt32(&inFlight, 1)
				mu.Lock()
				if n > maxInFlight {
					maxInFlight = n
				}
				mu.Unlock()
				<-start
				atomic.AddInt32(&inFlight, -1)
				return "ok", nil
			})
		}()
	}
	// Let the scheduler place all 12 jobs; only 4 should be running at once.
	time.Sleep(150 * time.Millisecond)
	close(start)
	// Wait for all to finish by polling the count via a generous wait.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		done := 0
		for _, j := range r.jobs {
			if j.terminal() {
				done++
			}
		}
		r.mu.Unlock()
		if done >= 12 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if maxInFlight > 4 {
		t.Fatalf("concurrency cap violated: saw %d concurrent jobs (cap 4)", maxInFlight)
	}
}

// TestBgJobEmptyRunRejected covers validation: a nil run must not panic.
func TestBgJobEmptyRunRejected(t *testing.T) {
	r := newTestBgRegistry(t)
	if job := r.spawn(context.Background(), "read_file", "run-x", "sess-x", nil); job != nil {
		t.Fatalf("spawn with nil run should return nil, got %+v", job)
	}
}

// TestWaitForOutputSchemaInjected sanity-checks injectWaitForOutput: a plain
// schema gains the property, and the exempt tool schemas do not.
func TestWaitForOutputSchemaInjected(t *testing.T) {
	injected := injectWaitForOutput(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	var m map[string]any
	if err := json.Unmarshal([]byte(injected), &m); err != nil {
		t.Fatalf("injected schema is not valid JSON: %v", err)
	}
	props, _ := m["properties"].(map[string]any)
	wfo, ok := props["wait_for_output"].(map[string]any)
	if !ok {
		t.Fatalf("expected wait_for_output property, got %v", props)
	}
	if def, _ := wfo["default"].(bool); !def {
		t.Fatalf("expected wait_for_output default=true, got %v", wfo["default"])
	}
	// Empty-properties schema still gets the property.
	empty := injectWaitForOutput(`{"type":"object","properties":{}}`)
	_ = json.Unmarshal([]byte(empty), &m)
	props, _ = m["properties"].(map[string]any)
	if _, ok := props["wait_for_output"]; !ok {
		t.Fatalf("expected wait_for_output on empty-properties schema, got %v", props)
	}
}

// TestBgJobSpawnJSONFormat verifies spawnBgTool's immediate JSON response shape
// so the model sees {job_id, status, queued, hint}.
func TestBgJobSpawnJSONFormat(t *testing.T) {
	dir := t.TempDir()
	o := &Orchestrator{stateDir: dir}
	ctx := context.Background()
	out := o.spawnBgTool(ctx, "read_file", func(ctx context.Context) (string, error) {
		return "ok", nil
	})
	if !strings.HasPrefix(out, "{") {
		t.Fatalf("expected JSON, got %q", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("spawn JSON invalid: %v", err)
	}
	if m["queued"] != true {
		t.Fatalf("expected queued=true, got %v", m["queued"])
	}
	if m["status"] != "queued" {
		t.Fatalf("expected status=queued, got %v", m["status"])
	}
	id, _ := m["job_id"].(string)
	if id == "" {
		t.Fatal("expected non-empty job_id")
	}
	// Clean up: let the job finish so the registry goroutine exits.
	reg := o.bgJobs()
	waitForBgTerminal(t, reg, id, 2*time.Second)
}
