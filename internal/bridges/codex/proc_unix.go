//go:build linux || darwin || freebsd || netbsd || openbsd

package codex

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the codex child in its own process group so the whole
// tree (including child shells spawned for command_execution) can be killed as
// a unit on cancellation. cmd.Cancel targets the negative PID (the group).
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID => signal the process group, not just the direct child
		// (contract §10). A child shell from command_execution dies with it.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			// Fall back to the direct child if the group kill failed (e.g. the
			// group was never established) so we never leak the process.
			return cmd.Process.Kill()
		}
		return nil
	}
}
