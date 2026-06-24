//go:build windows

package orchestrator

// exec_proc_windows.go is the Windows fallback. Windows has no Setpgid/process
// groups in the unix sense; we best-effort kill just the direct child. SapaLOQ
// targets linux for its runtime, so this exists only to keep cross-compilation
// green.

import "os/exec"

func setProcessGroup(_ *exec.Cmd) {}

func killProcessGroup(c *exec.Cmd) {
	if c == nil || c.Process == nil {
		return
	}
	_ = c.Process.Kill()
}
