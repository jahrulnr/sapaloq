package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Unrestricted host tool.
//
// By explicit design, SapaLOQ is NOT sandboxed to the workspace root. The
// workspace_* tools remain boundary-rooted for scoped, safe project edits, but
// system_exec gives the model full host access: run any command anywhere
// (which also covers reading any host file via cat/sed/head/tail/rg). This is
// intentional — the user does not want the assistant "kebiri" (crippled) to a
// single directory. It is available in every mode (Ask, planner, agent) via
// the shared-tool dispatcher.

// toolSystemExec runs an arbitrary shell command anywhere on the host with full
// access. Unlike terminal_run it does NOT pin the working directory to the
// workspace root: it defaults to the process CWD and honors an explicit cwd
// argument (any path). Output is byte-capped; a timeout guards runaway commands.
func toolSystemExec(ctx context.Context, args toolArgs) string {
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		return "Error: command is required."
	}
	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > maxTerminalSecs {
		timeout = maxTerminalSecs
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	c := exec.CommandContext(runCtx, "bash", "-lc", cmd)
	if dir := strings.TrimSpace(args.Cwd); dir != "" {
		c.Dir = expandHome(dir)
	}
	out, err := c.CombinedOutput()
	text := string(out)
	if len(text) > 16*1024 {
		text = text[:16*1024] + "\n[output truncated]"
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("Command timed out after %ds.\n%s", timeout, text)
	}
	if err != nil {
		return fmt.Sprintf("Command exited with error: %v\n%s", err, text)
	}
	if strings.TrimSpace(text) == "" {
		return "(command produced no output)"
	}
	return text
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
