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

// Unrestricted host tools.
//
// By explicit design, SapaLOQ is NOT sandboxed to the workspace root. The
// workspace_* tools remain boundary-rooted for scoped, safe project edits, but
// these system_* tools give the model full host access: read any file, run any
// command anywhere. This is intentional — the user does not want the assistant
// "kebiri" (crippled) to a single directory. They are available in every mode
// (Ask, planner, agent) via the shared-tool dispatcher.

const (
	systemReadDefaultBytes = 256 * 1024
	systemReadMaxBytes     = 4 * 1024 * 1024
)

// toolSystemReadFile reads any file on the host by absolute or relative path,
// with no workspace-boundary check. Relative paths resolve against the current
// process working directory. Binary content is refused (use system_exec with
// file/xxd/strings instead). A byte cap protects the context budget.
func toolSystemReadFile(args toolArgs) string {
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return "Error: path is required."
	}
	path = expandHome(path)
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "Error: " + err.Error()
	}
	if info.IsDir() {
		return fmt.Sprintf("Error: %q is a directory; use system_exec (e.g. `ls -la %s`).", path, path)
	}
	limit := args.MaxBytes
	if limit <= 0 {
		limit = systemReadDefaultBytes
	}
	if limit > systemReadMaxBytes {
		limit = systemReadMaxBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return "Error: " + err.Error()
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, _ := f.Read(buf)
	data := buf[:n]
	if looksBinary(data) {
		return fmt.Sprintf("Error: %q looks like a binary file (%d bytes). Refusing to read as text; use system_exec (e.g. `file`, `xxd`, `strings`).", path, info.Size())
	}
	content := string(data)

	// Line-range mode (offset/limit) mirrors workspace_read_file.
	if args.Offset > 0 || args.Limit > 0 {
		lines := strings.Split(content, "\n")
		start := args.Offset
		if start < 1 {
			start = 1
		}
		if start > len(lines) {
			return fmt.Sprintf("[no content: file has %d lines, offset %d is past end]", len(lines), start)
		}
		count := args.Limit
		if count <= 0 {
			count = 200
		}
		end := start - 1 + count
		if end > len(lines) {
			end = len(lines)
		}
		var b strings.Builder
		for i := start; i <= end; i++ {
			fmt.Fprintf(&b, "%d\t%s\n", i, lines[i-1])
		}
		return b.String()
	}

	if int64(n) < info.Size() {
		content += fmt.Sprintf("\n[truncated: read %d of %d bytes]", n, info.Size())
	}
	return content
}

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
