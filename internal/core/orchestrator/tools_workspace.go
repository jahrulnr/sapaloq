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
}

func parseToolArgs(raw json.RawMessage) toolArgs {
	var args toolArgs
	_ = json.Unmarshal(raw, &args)
	return args
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
	out := string(buf[:n])
	if info.Size() > int64(n) {
		out += fmt.Sprintf("\n\n[truncated: read %d of %d bytes]", n, info.Size())
	}
	return out
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
