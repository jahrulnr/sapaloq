package orchestrator

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const imageReadMaxBytes = 10 * 1024 * 1024 // 10 MiB cap for read_image payloads.

// imageMimeByExt maps common image extensions to their MIME types. This is the
// accepted image set across the bridges (png/jpg/jpeg/gif/webp).
var imageMimeByExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// toolReadImage reads an image file from any host path and returns it as an
// inline Markdown data-URI image: ![name](data:<mime>;base64,<payload>).
//
// This is how a local image becomes real vision input: the orchestrator's
// extractImages() scans messages for exactly this markdown and attaches the
// decoded image to the next inference turn's bridge.Request.Images (the same
// channel widget attachments use). Available in every mode via the shared
// dispatcher. Unlike workspace_read_file it does NOT reject binary content
// (images are binary by nature) and is NOT sandboxed to the workspace root.
func toolReadImage(args toolArgs) string {
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
		return fmt.Sprintf("Error: %q is a directory, not an image file.", path)
	}
	limit := imageReadMaxBytes
	if args.MaxBytes > 0 && args.MaxBytes < limit {
		limit = args.MaxBytes
	}
	if info.Size() > int64(limit) {
		return fmt.Sprintf("Error: %q is %d bytes, larger than the %d-byte read_image limit.", path, info.Size(), limit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "Error: " + err.Error()
	}
	mime := imageMimeFor(path, data)
	if mime == "" {
		return fmt.Sprintf("Error: %q does not look like a supported image (png, jpeg, gif, webp). Use workspace_read_file/system_exec for non-image files.", path)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	name := filepath.Base(path)
	return fmt.Sprintf("![%s](data:%s;base64,%s)", name, mime, encoded)
}

// imageMimeFor resolves a supported image MIME type from the file extension,
// falling back to content sniffing. Returns "" if it is not a supported image.
func imageMimeFor(path string, data []byte) string {
	if mime, ok := imageMimeByExt[strings.ToLower(filepath.Ext(path))]; ok {
		return mime
	}
	sniffed := http.DetectContentType(data)
	switch sniffed {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return sniffed
	}
	return ""
}

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
