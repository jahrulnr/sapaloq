//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package appserver

import "os/exec"

func setProcessGroup(*exec.Cmd) {}
func terminateProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
func killProcessGroup(cmd *exec.Cmd) error { return terminateProcessGroup(cmd) }
