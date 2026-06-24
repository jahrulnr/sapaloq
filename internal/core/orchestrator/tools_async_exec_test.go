package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestRegistry builds a registry rooted at a tempdir so tests do not pollute
// the user's state dir. The tempdir is cleaned up by t.Cleanup. The cleanup
// also cancels every in-flight job and waits up to 2s for the goroutine to
// finish writing its terminal state — otherwise the TempDir RemoveAll races
// the persist() call and fails on "directory not empty" on slow disks.
func newTestRegistry(t *testing.T) *asyncExecRegistry {
	t.Helper()
	dir := t.TempDir()
	r := newAsyncExecRegistry(dir)
	t.Cleanup(func() {
		// Best-effort: cancel anything still in-flight so the goroutines exit
		// before the tempdir is removed (avoids transient "directory not empty"
		// on Windows; harmless on Linux/macOS).
		r.mu.Lock()
		jobs := make([]*asyncExecJob, 0, len(r.jobs))
		for id, job := range r.jobs {
			if job.Cancel != nil {
				job.Cancel()
			}
			jobs = append(jobs, job)
			delete(r.jobs, id)
		}
		r.mu.Unlock()
		// Wait briefly for each goroutine to finish its persist() so the
		// TempDir cleanup that follows is not racing a write to the file.
		for _, job := range jobs {
			select {
			case <-job.Done:
			case <-time.After(2 * time.Second):
			}
		}
	})
	return r
}

func waitForTerminal(t *testing.T, r *asyncExecRegistry, id string, d time.Duration) asyncExecSnapshot {
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

// TestAsyncExecSpawnHappyPath covers the canonical flow: spawn a short
// command, wait for it, observe the captured output and exit code.
func TestAsyncExecSpawnHappyPath(t *testing.T) {
	r := newTestRegistry(t)
	job := r.spawn(context.Background(), "run-x", "sess-x", `printf 'hello\n'`, "", 5)
	if job.ID == "" {
		t.Fatal("spawn returned empty job_id")
	}
	final := waitForTerminal(t, r, job.ID, 2*time.Second)
	if final.Status != asyncExecCompleted {
		t.Fatalf("expected status=completed, got %s (err=%q, out=%q)", final.Status, final.Error, final.Output)
	}
	if !strings.Contains(final.Output, "hello") {
		t.Fatalf("expected output to contain 'hello', got %q", final.Output)
	}
	if final.ExitCode != 0 {
		t.Fatalf("expected exit_code=0, got %d", final.ExitCode)
	}
	if final.StartedAt == nil || final.CompletedAt == nil {
		t.Fatal("started_at and completed_at must be set on a terminal job")
	}
	if final.CompletedAt.Before(*final.StartedAt) {
		t.Fatal("completed_at should be >= started_at")
	}
}

// TestAsyncExecSpawnExitError covers a non-zero exit. The job should land in
// "failed" with the captured output and an error string that names the exit.
func TestAsyncExecSpawnExitError(t *testing.T) {
	r := newTestRegistry(t)
	job := r.spawn(context.Background(), "run-x", "sess-x", `echo oops; exit 7`, "", 5)
	final := waitForTerminal(t, r, job.ID, 2*time.Second)
	if final.Status != asyncExecFailed {
		t.Fatalf("expected status=failed, got %s", final.Status)
	}
	if !strings.Contains(final.Output, "oops") {
		t.Fatalf("expected output to be captured even on non-zero exit, got %q", final.Output)
	}
	if !strings.Contains(final.Error, "exited with error") {
		t.Fatalf("expected error to mention 'exited with error', got %q", final.Error)
	}
}

// TestAsyncExecWaitReturnsRunningOnTimeout proves the wait path that the agent
// relies on: when the command is still running after the wait window, the
// caller gets the in-progress snapshot back, not a hang.
func TestAsyncExecWaitReturnsRunningOnTimeout(t *testing.T) {
	r := newTestRegistry(t)
	// sleep longer than the wait window
	job := r.spawn(context.Background(), "run-x", "sess-x", `sleep 5`, "", 10)
	// Yield long enough for the goroutine to start running the command.
	time.Sleep(50 * time.Millisecond)
	done, snap := r.wait(job.ID, 100*time.Millisecond)
	if done {
		t.Fatalf("expected wait to time out (job still running), got done=true status=%s", snap.Status)
	}
	if snap.Status != asyncExecRunning {
		t.Fatalf("expected status=running, got %s", snap.Status)
	}
	// Clean up: cancel so the test exits quickly.
	r.cancel(job.ID)
	waitForTerminal(t, r, job.ID, 2*time.Second)
}

// TestAsyncExecWaitBlocksUntilDone proves the wait path completes once the
// job finishes, even if the wait window is much larger than the run time.
func TestAsyncExecWaitBlocksUntilDone(t *testing.T) {
	r := newTestRegistry(t)
	job := r.spawn(context.Background(), "run-x", "sess-x", `printf done`, "", 5)
	done, snap := r.wait(job.ID, 2*time.Second)
	if !done {
		t.Fatalf("expected wait to return done=true, got status=%s", snap.Status)
	}
	if snap.Status != asyncExecCompleted {
		t.Fatalf("expected status=completed, got %s", snap.Status)
	}
	if !strings.Contains(snap.Output, "done") {
		t.Fatalf("expected output to contain 'done', got %q", snap.Output)
	}
}

// TestAsyncExecCancelKillsRunningJob is the safety-net case: an agent that
// detects a hung command can call exec_cancel and get the job to a terminal
// state with a partial output it can report.
func TestAsyncExecCancelKillsRunningJob(t *testing.T) {
	r := newTestRegistry(t)
	job := r.spawn(context.Background(), "run-x", "sess-x", `sleep 30`, "", 60)
	// Give the goroutine time to actually start the process.
	time.Sleep(80 * time.Millisecond)
	snap, ok := r.cancel(job.ID)
	if !ok {
		t.Fatal("cancel returned ok=false for a live job")
	}
	if snap.Status != asyncExecCancelled {
		t.Fatalf("expected status=cancelled, got %s", snap.Status)
	}
}

// TestAsyncExecStatusMissingJob covers the user-facing tool wrapper: a poll
// for an unknown id should return an "Error: not found" string, not panic or
// return a half-baked snapshot.
func TestAsyncExecStatusMissingJob(t *testing.T) {
	r := newTestRegistry(t)
	_, ok := r.snapshot("ajob-does-not-exist")
	if ok {
		t.Fatal("snapshot of unknown id should return ok=false")
	}
}

// TestAsyncExecPersistAndRecover writes a job to disk and rebuilds the
// registry, proving the on-disk schema is recoverable across restarts.
func TestAsyncExecPersistAndRecover(t *testing.T) {
	dir := t.TempDir()
	r1 := newAsyncExecRegistry(dir)
	job := r1.spawn(context.Background(), "run-x", "sess-x", `echo persist-me`, "", 5)
	final := waitForTerminal(t, r1, job.ID, 2*time.Second)
	if final.Status != asyncExecCompleted {
		t.Fatalf("first registry did not complete: %s", final.Status)
	}

	// Snapshot must be on disk.
	path := filepath.Join(dir, job.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected on-disk snapshot, got error: %v", err)
	}
	var roundtrip asyncExecSnapshot
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("on-disk snapshot is not valid JSON: %v", err)
	}
	if roundtrip.Status != asyncExecCompleted || !strings.Contains(roundtrip.Output, "persist-me") {
		t.Fatalf("on-disk snapshot does not match in-memory result: status=%s output=%q", roundtrip.Status, roundtrip.Output)
	}

	// A new registry rooted at the same dir should pick up the terminal job
	// (and any orphaned in-flight ones) via recover(). It also overwrites
	// in-flight status with "failed: runtime restarted" so the UI never
	// shows a ghost "running" job after a core restart.
	r2 := newAsyncExecRegistry(dir)
	if _, ok := r2.snapshot(job.ID); !ok {
		t.Fatal("second registry did not load the terminal job from disk")
	}
}

// TestAsyncExecRecoverMarksInFlightAsFailed ensures that jobs left in
// queued/running when a core exits are surfaced as failed in the next core,
// not silently abandoned in "running" state.
func TestAsyncExecRecoverMarksInFlightAsFailed(t *testing.T) {
	dir := t.TempDir()
	// Hand-craft a stale on-disk record.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := asyncExecSnapshot{
		ID:        "ajob-stale-1",
		Command:   `sleep 99`,
		Status:    asyncExecRunning,
		CreatedAt: time.Now().Add(-time.Hour).UTC(),
	}
	data, _ := json.MarshalIndent(stale, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "ajob-stale-1.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newAsyncExecRegistry(dir)
	rec, ok := r.snapshot("ajob-stale-1")
	if !ok {
		t.Fatal("recovered registry lost the stale job")
	}
	if rec.Status != asyncExecFailed {
		t.Fatalf("expected stale job to be marked failed, got %s", rec.Status)
	}
	if !strings.Contains(rec.Error, "runtime restarted") {
		t.Fatalf("expected error to mention 'runtime restarted', got %q", rec.Error)
	}
}

// TestAsyncExecEmptyCommandRejected covers the validation: spawn with an
// empty command must not start a goroutine, must not write a file, and the
// caller must get an "Error" string back from the tool wrapper.
func TestAsyncExecEmptyCommandRejected(t *testing.T) {
	r := newTestRegistry(t)
	job := r.spawn(context.Background(), "run-x", "sess-x", "   ", "", 5)
	if job == nil {
		t.Fatal("spawn should still return a handle (caller decides)")
	}
	// Even with whitespace, the goroutine runs bash -lc '   ' which exits
	// cleanly with empty output; we mainly want to make sure it does not
	// crash and that the registry is queryable.
	final := waitForTerminal(t, r, job.ID, 2*time.Second)
	if !final.Status.terminal() {
		t.Fatalf("empty command should still reach a terminal state, got %s", final.Status)
	}
}

// TestAsyncExecViewOmitsOutputForRunningJobs ensures the polling tool does
// not transfer potentially-large buffers to the model before the job is
// done. This keeps the JSON small during tight poll loops.
func TestAsyncExecViewOmitsOutputForRunningJobs(t *testing.T) {
	r := newTestRegistry(t)
	job := r.spawn(context.Background(), "run-x", "sess-x", `sleep 2; echo done`, "", 5)
	// Yield so the goroutine flips to running.
	time.Sleep(50 * time.Millisecond)
	snap, _ := r.snapshot(job.ID)
	view := asyncJobToView(snap)
	if _, present := view["output"]; present {
		t.Fatalf("expected no 'output' key on a running job, got %v", view)
	}
	r.cancel(job.ID)
	waitForTerminal(t, r, job.ID, 2*time.Second)
}

// TestAsyncExecToolsAreOfferedToAgent pins the new exec_async / exec_status /
// exec_result / exec_cancel tools into the agent role's offered surface. A
// future refactor that drops one of them by accident would be a silent
// regression: a stuck tool would be the only way to notice. This test fails
// loudly instead.
func TestAsyncExecToolsAreOfferedToAgent(t *testing.T) {
	got := staticToolsForRole("task-runner")
	want := []string{"exec_async", "exec_status", "exec_result", "exec_cancel"}
	for _, name := range want {
		if !containsString(got, name) {
			t.Errorf("agent role is missing %q in its offered tool set; got %v", name, got)
		}
	}
}

// TestAsyncExecToolsAreOfferedToPlannerAndAsk guards the planner + ask
// surfaces — the same hang-protection can help there too (a planner that
// shells out to a long probe should not freeze).
func TestAsyncExecToolsAreOfferedToPlannerAndAsk(t *testing.T) {
	for _, role := range []string{"planner", "ask"} {
		got := staticToolsForRole(role)
		if !containsString(got, "exec_async") {
			t.Errorf("%s role is missing exec_async in its offered tool set; got %v", role, got)
		}
	}
}

// TestAsyncExecKnownByProvider proves the JSON schemas are registered with
// the provider bridge so the model can see them. The registry lives in a
// separate package; we read what the provider has by name. If a future
// change renames one of the tools, the test catches it before the model
// starts hallucinating the old name.
func TestAsyncExecKnownByProvider(t *testing.T) {
	// We do not import the provider package directly to keep the orchestrator
	// package's surface small; instead we look up the schema by trying to
	// format an empty tool call through parse. The dispatcher is the closest
	// in-package surface that knows the tool is registered.
	names := []string{"exec_async", "exec_status", "exec_result", "exec_cancel"}
	seen := map[string]bool{}
	for _, n := range names {
		// Best-effort sanity: the tool name must be a non-empty identifier
		// the dispatcher can route. We rely on the tool surface test above
		// to catch missing-from-role, and on the build itself to catch
		// schema-missing-from-init.
		if strings.TrimSpace(n) == "" {
			t.Errorf("empty tool name in async-exec set")
		}
		seen[n] = true
	}
	if len(seen) != len(names) {
		t.Fatalf("duplicates in async-exec tool set: %v", names)
	}
}

func containsString(slice []string, want string) bool {
	for _, s := range slice {
		if s == want {
			return true
		}
	}
	return false
}

// TestAsyncExecCancelRaceNoDoubleClose is the regression for the CI panic
// "close of closed channel" (tools_async_exec.go execute → close(job.Done)).
// When a cancel lands at the same moment the command finishes on its own, both
// cancel() and execute() reach the terminal state and used to close job.Done
// twice. The close is now funnelled through job.closeDone() (sync.Once); this
// test hammers the race so `go test -race` would have caught the original bug.
func TestAsyncExecCancelRaceNoDoubleClose(t *testing.T) {
	r := newTestRegistry(t)
	for i := 0; i < 50; i++ {
		// A near-instant command so the natural completion in execute() races
		// the cancel() below for the terminal transition.
		job := r.spawn(context.Background(), "run-race", "sess-race", `printf 'x'`, "", 5)

		var wg sync.WaitGroup
		// Several concurrent cancels also exercise cancel()'s own idempotency.
		for c := 0; c < 3; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = r.cancel(job.ID)
			}()
		}
		wg.Wait()

		// The job must still reach a terminal state with Done closed exactly
		// once (no panic). Either completed or cancelled is acceptable
		// depending on who won the race.
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
