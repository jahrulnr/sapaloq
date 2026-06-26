//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package codex

import "os/exec"

// setProcessGroup is a no-op on platforms without POSIX process groups. The
// codex CLI is effectively Unix-only, but keeping a portable stub lets the
// package build everywhere (e.g. Windows CI) without conditional imports.
func setProcessGroup(cmd *exec.Cmd) {}
