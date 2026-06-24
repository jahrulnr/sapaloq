//go:build !windows

package orchestrator

// exec_proc_unix.go implements process-group control on unix-like systems.
// Launching the command with Setpgid puts it (and every child it spawns,
// including ones detached with `&`) into a fresh process group whose ID equals
// the bash PID. Killing the negative PID then signals the entire group, so a
// backgrounded `python3 -m http.server &` dies along with its bash parent.

import (
	"os/exec"
	"syscall"
)

// setProcessGroup makes the command the leader of a new process group.
func setProcessGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs the whole process group led by c. Falls back to
// killing just the process if the group signal fails (e.g. the leader already
// exited). Safe to call when the process never started.
func killProcessGroup(c *exec.Cmd) {
	if c == nil || c.Process == nil {
		return
	}
	pid := c.Process.Pid
	// Negative pid targets the process group whose PGID == pid (set via
	// Setpgid above). This reaps detached `&` children too.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = c.Process.Kill()
	}
}
