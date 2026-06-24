//go:build !windows

package orchestrator

// exec_run_test.go locks in the fix for the historical "tool_call stuck" bug:
// a command that spawns a background process (e.g. `python3 -m http.server &`)
// must NOT wedge the exec call. Before the process-group fix, CombinedOutput()
// blocked forever because the detached child inherited the output pipe.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// processAlive reports whether a pid is still running (signal 0 probe).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Signal 0 performs error checking without sending a signal.
	err := syscall.Kill(pid, 0)
	return err == nil
}

// TestRunShellCapturedBackgroundDoesNotHang is the core regression: a command
// that backgrounds a long-lived child must still return promptly (the child
// inheriting the pipe used to block CombinedOutput forever).
func TestRunShellCapturedBackgroundDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	// Background a sleeper that records its own PID, then the foreground part
	// returns immediately. Naively this hangs because the sleeper keeps the
	// inherited stdout open.
	cmd := fmt.Sprintf(`( sleep 60 & echo $! > %q ); echo started`, pidFile)

	start := time.Now()
	res := runShellCaptured(context.Background(), cmd, dir, 30*time.Second)
	elapsed := time.Since(start)

	// With the fix the foreground command finishes and we return in well under
	// a second; the 5s bound is generous slack for slow CI.
	if elapsed > 5*time.Second {
		t.Fatalf("runShellCaptured did not return promptly for a backgrounded child: took %s (timed_out=%v)", elapsed, res.TimedOut)
	}
	if res.TimedOut {
		t.Fatalf("backgrounded child should not cause a timeout when the foreground command exits; output=%q", res.Output)
	}
	if !strings.Contains(res.Output, "started") {
		t.Fatalf("expected foreground output 'started', got %q", res.Output)
	}
}

// TestRunShellCapturedTimeoutKillsGroup proves that when the command itself
// runs past the timeout, we (a) return at the deadline and (b) kill the whole
// process group so a backgrounded child is reaped too - not left running.
func TestRunShellCapturedTimeoutKillsGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	// Background a long sleeper, record its pid, then block in the foreground
	// so the whole command exceeds the timeout and must be killed.
	cmd := fmt.Sprintf(`sleep 120 & echo $! > %q; wait`, pidFile)

	start := time.Now()
	res := runShellCaptured(context.Background(), cmd, dir, 1*time.Second)
	elapsed := time.Since(start)

	if !res.TimedOut {
		t.Fatalf("expected TimedOut=true, got %+v", res)
	}
	// Should return close to the 1s deadline (+ the helper's small grace), not
	// hang for the full 120s sleep.
	if elapsed > 8*time.Second {
		t.Fatalf("timeout took too long to return: %s", elapsed)
	}

	// The backgrounded sleeper must have been killed with the group.
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child never recorded its pid: %v", err)
	}
	pid := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &pid); err != nil || pid <= 0 {
		t.Fatalf("bad pid in file %q: %v", string(raw), err)
	}
	// Give the kill a brief moment to take effect.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return // success: child reaped with the group
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Clean up the leak so we do not strand the sleeper, then fail.
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("backgrounded child pid %d survived the process-group kill (leak)", pid)
}

// TestRunShellCapturedCancelKillsGroup proves host cancellation (the path
// exec_cancel uses via the job's context) also tears down the whole group.
func TestRunShellCapturedCancelKillsGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	cmd := fmt.Sprintf(`sleep 120 & echo $! > %q; wait`, pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	type out struct {
		res     execResult
		elapsed time.Duration
	}
	ch := make(chan out, 1)
	go func() {
		start := time.Now()
		res := runShellCaptured(ctx, cmd, dir, 60*time.Second)
		ch <- out{res, time.Since(start)}
	}()

	// Let the child register its pid, then cancel.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case got := <-ch:
		if !got.res.Cancelled {
			t.Fatalf("expected Cancelled=true, got %+v", got.res)
		}
		if got.elapsed > 8*time.Second {
			t.Fatalf("cancel took too long to return: %s", got.elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runShellCaptured did not return after cancel (still hung)")
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child never recorded its pid: %v", err)
	}
	pid := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &pid); err != nil || pid <= 0 {
		t.Fatalf("bad pid in file %q: %v", string(raw), err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("backgrounded child pid %d survived cancel (leak)", pid)
}

// TestAsyncExecBackgroundReachesTerminal ties the fix back to the async path:
// an exec_async whose command backgrounds a child must still reach a terminal
// state and not sit in "running" forever (the on-screen "stuck" symptom).
func TestAsyncExecBackgroundReachesTerminal(t *testing.T) {
	r := newTestRegistry(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	cmd := fmt.Sprintf(`( sleep 60 & echo $! > %q ); echo ok`, pidFile)
	job := r.spawn(context.Background(), "run-x", "sess-x", cmd, dir, 10)
	final := waitForTerminal(t, r, job.ID, 5*time.Second)
	if final.Status != asyncExecCompleted {
		t.Fatalf("expected completed, got %s (err=%q out=%q)", final.Status, final.Error, final.Output)
	}
	if !strings.Contains(final.Output, "ok") {
		t.Fatalf("expected output 'ok', got %q", final.Output)
	}
}

// TestToolExecBackgroundReturns is the sync-tool mirror: the user-facing
// `exec` tool must return (not hang) when the command backgrounds a child.
func TestToolExecBackgroundReturns(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	o := &Orchestrator{}
	args := toolArgs{
		Command: fmt.Sprintf(`( sleep 60 & echo $! > %q ); echo done`, pidFile),
		Cwd:     dir,
	}
	doneCh := make(chan string, 1)
	go func() { doneCh <- o.toolExec(context.Background(), args) }()
	select {
	case out := <-doneCh:
		if !strings.Contains(out, "done") {
			t.Fatalf("expected 'done' in output, got %q", out)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("toolExec hung on a backgrounded child")
	}
}
