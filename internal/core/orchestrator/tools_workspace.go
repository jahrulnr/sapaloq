package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// workspaceRoot is the sandbox root for workspace_* tools: the directory the
// core process was launched from. All paths are resolved against it and any
// attempt to escape it is rejected.
func workspaceRoot() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// resolveInWorkspace joins rel against the workspace root and verifies the
// result stays within the root (no traversal via .. or symlink-style escapes
// on the lexical path).
func resolveInWorkspace(rel string) (string, error) {
	root := workspaceRoot()
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rel = strings.TrimSpace(rel)
	if rel == "" {
		rel = "."
	}
	var joined string
	if filepath.IsAbs(rel) {
		joined = filepath.Clean(rel)
	} else {
		joined = filepath.Clean(filepath.Join(rootAbs, rel))
	}
	relCheck, err := filepath.Rel(rootAbs, joined)
	if err != nil {
		return "", err
	}
	if relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the workspace root", rel)
	}
	return joined, nil
}

const (
	defaultReadBytes = 64 * 1024
	maxReadBytes     = 512 * 1024
	maxSearchResults = 40
	maxListEntries   = 200
	maxTerminalSecs  = 600
)

// toolArgs is the union of arguments any tool may receive. JSON unmarshalling
// ignores unknown fields, so a single struct keeps the dispatcher simple.
type toolArgs struct {
	Path           string   `json:"path"`
	MaxBytes       int      `json:"max_bytes"`
	Offset         int      `json:"offset"`     // read_file: 1-based start line
	Limit          int      `json:"limit"`      // read_file: max lines to read
	OldString      string   `json:"old_string"` // edit_file: text to replace
	NewString      string   `json:"new_string"` // edit_file: replacement text
	ReplaceAll     bool     `json:"replace_all"`
	Pattern        string   `json:"pattern"`
	Glob           string   `json:"glob"`
	MaxResults     int      `json:"max_results"`
	Query          string   `json:"query"`
	URL            string   `json:"url"`
	Content        string   `json:"content"`
	Command        string   `json:"command"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	Markdown       string   `json:"markdown"`
	Note           string   `json:"note"`
	Summary        string   `json:"summary"`
	Reason         string   `json:"reason"`
	Question       string   `json:"question"`
	Options        []string `json:"options"`
	StorageID      string   `json:"storage_id"` // scribe_write_note: explicit storage path id
	Intent         string   `json:"intent"`     // scribe_write_note: intent phrase → path
	Mode           string   `json:"mode"`       // scribe_write_note: personal|work|hobby
	Kind           string   `json:"kind"`       // scribe_write_note: notes|inbox|journal…
}

func parseToolArgs(raw json.RawMessage) toolArgs {
	var args toolArgs
	_ = json.Unmarshal(raw, &args)
	return args
}

// looksBinary reports whether a byte chunk is likely binary: a NUL byte is a
// strong signal, and a high ratio of non-printable bytes is a softer one. This
// guards read_file from dumping raw binary as a "string" (a real failure mode
// the user flagged — read may otherwise happily read a 1GB or binary file).
func looksBinary(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	nonPrintable := 0
	for _, c := range b {
		if c == 0 {
			return true
		}
		// Allow common whitespace control chars; count the rest as non-printable.
		if c < 0x09 || (c > 0x0d && c < 0x20) {
			nonPrintable++
		}
	}
	return float64(nonPrintable)/float64(len(b)) > 0.30
}

func toolReadFile(args toolArgs) string {
	abs, err := resolveInWorkspace(args.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "Error: " + err.Error()
	}
	if info.IsDir() {
		return fmt.Sprintf("Error: %q is a directory; use workspace_list_dir.", args.Path)
	}

	limit := args.MaxBytes
	if limit <= 0 {
		limit = defaultReadBytes
	}
	if limit > maxReadBytes {
		limit = maxReadBytes
	}
	f, err := os.Open(abs)
	if err != nil {
		return "Error: " + err.Error()
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, _ := f.Read(buf)
	data := buf[:n]

	// Binary guard: never return raw binary content as text.
	if looksBinary(data) {
		return fmt.Sprintf("Error: %q looks like a binary file (%d bytes). Refusing to read as text; use terminal_run (e.g. `file`, `xxd`, `strings`) if you need to inspect it.", args.Path, info.Size())
	}

	content := string(data)

	// Line-range mode (cursor-style): when offset/limit are given, return only
	// the requested 1-based line window with line numbers. Otherwise return the
	// (byte-capped) whole content.
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
		for i := start - 1; i < end; i++ {
			b.WriteString(fmt.Sprintf("%d\t%s\n", i+1, lines[i]))
		}
		out := b.String()
		if end < len(lines) || info.Size() > int64(n) {
			out += fmt.Sprintf("\n[showing lines %d-%d of %d%s]", start, end, len(lines), byteTruncNote(info.Size(), n))
		}
		return out
	}

	if info.Size() > int64(n) {
		content += fmt.Sprintf("\n\n[truncated: read %d of %d bytes]", n, info.Size())
	}
	return content
}

func byteTruncNote(size int64, n int) string {
	if size > int64(n) {
		return fmt.Sprintf("; byte-capped at %d/%d", n, size)
	}
	return ""
}

// toolEditFile performs a precise in-place string replacement (cursor-style
// edit), instead of overwriting the whole file. old_string must be unique
// unless replace_all is set. The write is atomic.
func toolEditFile(args toolArgs) string {
	abs, err := resolveInWorkspace(args.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	if args.OldString == "" {
		return "Error: old_string is required (use workspace_create_file for new files)."
	}
	if args.OldString == args.NewString {
		return "Error: old_string and new_string are identical; nothing to change."
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "Error: " + err.Error()
	}
	if info.IsDir() {
		return fmt.Sprintf("Error: %q is a directory.", args.Path)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "Error: " + err.Error()
	}
	if looksBinary(raw) {
		return fmt.Sprintf("Error: %q looks binary; refusing to edit.", args.Path)
	}
	content := string(raw)
	occurrences := strings.Count(content, args.OldString)
	if occurrences == 0 {
		return "Error: old_string not found in the file. Read the file first and copy the exact text (including whitespace)."
	}
	if occurrences > 1 && !args.ReplaceAll {
		return fmt.Sprintf("Error: old_string occurs %d times; provide more surrounding context to make it unique, or set replace_all=true.", occurrences)
	}
	var updated string
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, args.OldString, args.NewString)
	} else {
		updated = strings.Replace(content, args.OldString, args.NewString, 1)
	}
	if err := writeFileAtomic(abs, []byte(updated), info.Mode().Perm()); err != nil {
		return "Error: " + err.Error()
	}
	n := occurrences
	if !args.ReplaceAll {
		n = 1
	}
	return fmt.Sprintf("Edited %s (%d replacement(s)).", args.Path, n)
}

// toolDeleteFile removes a file within the sandbox. Directories are rejected to
// avoid accidental recursive deletes.
func toolDeleteFile(args toolArgs) string {
	abs, err := resolveInWorkspace(args.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "Error: " + err.Error()
	}
	if info.IsDir() {
		return fmt.Sprintf("Error: %q is a directory; refusing to delete directories.", args.Path)
	}
	if err := os.Remove(abs); err != nil {
		return "Error: " + err.Error()
	}
	return fmt.Sprintf("Deleted %s.", args.Path)
}

// toolGlob lists files matching a glob pattern under the workspace root. It
// supports "**" for recursive matching. Native — no shell needed.
func toolGlob(args toolArgs) string {
	pattern := strings.TrimSpace(args.Pattern)
	if pattern == "" {
		return "Error: pattern is required."
	}
	root := workspaceRoot()
	limit := args.MaxResults
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	recursive := strings.Contains(pattern, "**")
	// Reduce "**/" to a base pattern matched against each path's basename or
	// relative path. We do a manual walk so "**" works regardless of OS glob.
	var matches []string
	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || count >= limit {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if globMatch(pattern, rel, recursive) {
			matches = append(matches, rel)
			count++
		}
		return nil
	})
	if len(matches) == 0 {
		return "No files match."
	}
	sort.Strings(matches)
	out := strings.Join(matches, "\n")
	if count >= limit {
		out += fmt.Sprintf("\n[capped at %d results]", limit)
	}
	return out
}

// globMatch matches rel against pattern. With recursive=true ("**" present) it
// matches the trailing segment pattern against the basename and the full rel
// path; otherwise it uses filepath.Match on the basename and the rel path.
func globMatch(pattern, rel string, recursive bool) bool {
	base := filepath.Base(rel)
	if recursive {
		// Strip leading "**/" and match the remainder against the basename and
		// the full relative path.
		trimmed := strings.TrimPrefix(pattern, "**/")
		trimmed = strings.ReplaceAll(trimmed, "**/", "")
		if ok, _ := filepath.Match(trimmed, base); ok {
			return true
		}
		if ok, _ := filepath.Match(trimmed, rel); ok {
			return true
		}
		return false
	}
	if ok, _ := filepath.Match(pattern, base); ok {
		return true
	}
	if ok, _ := filepath.Match(pattern, rel); ok {
		return true
	}
	return false
}

func toolListDir(args toolArgs) string {
	abs, err := resolveInWorkspace(args.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "Error: " + err.Error()
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var b strings.Builder
	count := 0
	for _, e := range entries {
		if count >= maxListEntries {
			b.WriteString(fmt.Sprintf("… (%d more)\n", len(entries)-count))
			break
		}
		if e.IsDir() {
			b.WriteString(e.Name() + "/\n")
		} else {
			b.WriteString(e.Name() + "\n")
		}
		count++
	}
	if b.Len() == 0 {
		return "(empty directory)"
	}
	return b.String()
}

func toolSearch(args toolArgs) string {
	if strings.TrimSpace(args.Pattern) == "" {
		return "Error: pattern is required."
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return "Error: invalid regex: " + err.Error()
	}
	root := workspaceRoot()
	limit := args.MaxResults
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	var results []string
	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || count >= limit {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if args.Glob != "" {
			if ok, _ := filepath.Match(args.Glob, d.Name()); !ok {
				return nil
			}
		}
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() > 2*1024*1024 {
			return nil
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(root, path)
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		line := 0
		for scanner.Scan() {
			line++
			if re.MatchString(scanner.Text()) {
				text := strings.TrimSpace(scanner.Text())
				if len(text) > 200 {
					text = text[:200] + "…"
				}
				results = append(results, fmt.Sprintf("%s:%d: %s", rel, line, text))
				count++
				if count >= limit {
					break
				}
			}
		}
		return nil
	})
	if len(results) == 0 {
		return "No matches."
	}
	return strings.Join(results, "\n")
}

func toolWriteFile(args toolArgs, mustNotExist bool) string {
	abs, err := resolveInWorkspace(args.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	if mustNotExist {
		if _, statErr := os.Stat(abs); statErr == nil {
			return fmt.Sprintf("Error: %q already exists; use workspace_write_file to overwrite.", args.Path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "Error: " + err.Error()
	}
	if err := os.WriteFile(abs, []byte(args.Content), 0o644); err != nil {
		return "Error: " + err.Error()
	}
	return fmt.Sprintf("Wrote %d bytes to %s.", len(args.Content), args.Path)
}

func toolTerminalRun(ctx context.Context, args toolArgs) string {
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
	c.Dir = workspaceRoot()
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
