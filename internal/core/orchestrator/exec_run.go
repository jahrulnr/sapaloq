package orchestrator

// exec_run.go centralizes how SapaLOQ runs an arbitrary shell command and
// captures its output. Both the synchronous `exec` tool and the async
// exec registry funnel through runShellCaptured so the hard-won fixes live in
// one place.
//
// The recurring "tool_call stuck" bug came from running commands that spawn a
// background process, e.g. `python3 -m http.server 8080 &`. With the naive
// exec.CommandContext(...).CombinedOutput() approach two things went wrong:
//
//  1. CombinedOutput() wires a single pipe that the backgrounded child
//     inherits. Wait() then blocks until *every* writer closes the pipe -
//     including the http.server that runs forever - so the call never returns.
//  2. CommandContext's timeout only signals the direct child (`bash`). The
//     detached background grandchild keeps the pipe open, so even after the
//     deadline fires the goroutine stays wedged past the timeout.
//
// runShellCaptured fixes both by (a) launching the command in its own process
// group (Setpgid on unix), (b) reading output through pipes we own, and (c) on
// timeout/cancel killing the *entire* process group so detached children die
// and the pipes close, letting the function return promptly with whatever
// output was captured.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// execResult is the outcome of runShellCaptured.
type execResult struct {
	// Output is the combined stdout+stderr captured up to completion or kill.
	Output string
	// FinalCWD is the working directory reported by the cwd marker, if present.
	FinalCWD string
	// TimedOut is true when the run was terminated because the timeout elapsed.
	TimedOut bool
	// Cancelled is true when the parent ctx was cancelled (host cancel).
	Cancelled bool
	// Err is the command's wait error (nil on clean exit, or after a kill).
	Err error
}

const execCWDMarker = "__SAPALOQ_FINAL_CWD__="

// runShellCaptured runs cmd via `bash -lc` in its own process group, capturing
// combined output. It never blocks longer than timeout: when the deadline or
// the parent ctx fires it kills the whole process group (so backgrounded
// children spawned with `&` are reaped too) and returns the partial output.
func runShellCaptured(ctx context.Context, cmd, cwd string, timeout time.Duration) execResult {
	wrapped := cmd + "\nstatus=$?\nprintf '\\n" + execCWDMarker + "%s\\n' \"$PWD\"\nexit $status"

	// We do NOT use exec.CommandContext: its default Cancel only kills the
	// direct child. We manage the lifecycle ourselves so we can kill the
	// whole process group.
	c := exec.Command("bash", "-lc", wrapped)
	if dir := strings.TrimSpace(cwd); dir != "" {
		c.Dir = expandHome(dir)
	}
	setProcessGroup(c)

	// Use a real OS pipe (not io.Pipe / a bytes.Buffer) for stdout+stderr.
	// Critical: when c.Stdout is an *os.File, Cmd.Wait does NOT spawn an
	// internal copier goroutine that would block on the pipe's read end. That
	// copier is exactly what made the old code hang: a backgrounded `&` child
	// inherits the write end, so the pipe never hits EOF and Wait blocked
	// forever. With our own *os.File we close the parent's write end after
	// Start and read the read end on our own schedule; Wait returns as soon as
	// bash itself exits, regardless of detached children.
	pr, pw, err := os.Pipe()
	if err != nil {
		return execResult{Err: fmt.Errorf("pipe: %w", err)}
	}
	c.Stdout = pw
	c.Stderr = pw

	if err := c.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return execResult{Err: fmt.Errorf("start: %w", err)}
	}
	// The child has inherited the write end; the parent must close its own
	// copy so that once every process holding the write end exits, the reader
	// observes EOF.
	_ = pw.Close()

	var buf bytes.Buffer
	var bufMu sync.Mutex
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		drainBuf := make([]byte, 32*1024)
		for {
			n, rerr := pr.Read(drainBuf)
			if n > 0 {
				bufMu.Lock()
				buf.Write(drainBuf[:n])
				bufMu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- c.Wait()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	res := execResult{}
	select {
	case err := <-waitErr:
		res.Err = err
	case <-timer.C:
		res.TimedOut = true
		killProcessGroup(c)
	case <-ctx.Done():
		res.Cancelled = true
		killProcessGroup(c)
	}

	if res.TimedOut || res.Cancelled {
		// After killing the group, give bash a brief moment to be reaped so we
		// can capture its wait error; then force the read end closed so the
		// reader goroutine unblocks even if some detached fd lingers.
		select {
		case res.Err = <-waitErr:
		case <-time.After(2 * time.Second):
		}
		_ = pr.Close()
	}

	// Wait for the reader to flush whatever it has, bounded so a stubborn fd
	// can never wedge us.
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
	}
	_ = pr.Close()

	bufMu.Lock()
	raw := buf.String()
	bufMu.Unlock()

	text, finalCWD := splitExecCWD(raw, execCWDMarker)
	if len(text) > 16*1024 {
		text = text[:16*1024] + "\n[output truncated]"
	}
	res.Output = text
	res.FinalCWD = finalCWD
	return res
}
