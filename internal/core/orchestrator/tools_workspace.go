package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// resolvePath turns a user-supplied path into an absolute host path. SapaLOQ is
// unrestricted by design: there is no workspace sandbox. A leading ~ expands to
// the home directory and relative paths resolve against the process CWD. Any
// path on the host is permitted.
func resolvePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = "."
	}
	p = expandHome(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// toolSearchRoot is the directory work tools walk when listing/searching/globbing.
// resolveActorArgs sets Cwd to the actor workspace; Path overrides when explicit.
func toolSearchRoot(args toolArgs) (string, error) {
	p := strings.TrimSpace(args.Path)
	if p == "" {
		p = strings.TrimSpace(args.Cwd)
	}
	return resolvePath(p)
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
	Cwd            string   `json:"cwd"` // exec: optional working dir
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
	Title          string   `json:"title"`      // desktop_notify: notification title
	Body           string   `json:"body"`       // desktop_notify: notification body
	Urgency        string   `json:"urgency"`    // desktop_notify: low|normal|critical
	TargetTaskID   string   `json:"target_task_id"`
	Message        string   `json:"message"`
	Priority       string   `json:"priority"`
	CorrelationID  string   `json:"correlation_id"`
	JobID          string   `json:"job_id"`                    // wait / sapaloq_cancel_job target
	WaitSeconds    int      `json:"wait_seconds"`              // exec_result: optional poll window (0 = immediate)
	Seconds        int      `json:"seconds"`                   // wait(time): sleep duration
	TaskID         string   `json:"task_id"`                   // wait(task): sub-agent task to watch
	Task           string   `json:"task"`                      // actor spawn intent
	PlanTaskID     string   `json:"plan_task_id"`              // planner hand-off
	Scope          string   `json:"scope"`                     // stop scope
	Answer         string   `json:"answer"`                    // clarification answer
	WaitForOutput  *bool    `json:"wait_for_output,omitempty"` // fire-and-forget when false; nil defaults to true
}

func parseToolArgs(raw json.RawMessage) toolArgs {
	var args toolArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		// Models frequently emit multi-line argument values (heredoc bodies,
		// file content) with RAW control bytes inside the JSON string, which is
		// invalid JSON and makes encoding/json drop the value silently - the
		// tool then sees empty args and the model wrongly concludes its content
		// was "stripped". Repair the raw control chars and retry once.
		_ = json.Unmarshal(parse.RepairControlCharsInJSON(raw), &args)
	}
	return args
}

// looksBinary reports whether a byte chunk is likely binary: a NUL byte is a
// strong signal, and a high ratio of non-printable bytes is a softer one. This
// guards read_file from dumping raw binary as a "string" (a real failure mode
// the user flagged - read may otherwise happily read a 1GB or binary file).
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
	abs, err := resolvePath(args.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "Error: " + err.Error()
	}
	if info.IsDir() {
		return fmt.Sprintf("Error: %q is a directory; use list_dir.", args.Path)
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
		return fmt.Sprintf("Error: %q looks like a binary file (%d bytes). Refusing to read as text; use exec (e.g. `file`, `xxd`, `strings`) if you need to inspect it.", args.Path, info.Size())
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
	abs, err := resolvePath(args.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	if args.OldString == "" {
		return "Error: old_string is required (use create_file for new files)."
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

// toolDeleteFile removes a single file at any host path. Directories are
// rejected to avoid accidental recursive deletes.
func toolDeleteFile(args toolArgs) string {
	abs, err := resolvePath(args.Path)
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

// toolGlob lists files matching a glob pattern under a root directory. Uses
// ripgrep-inspired pruning (gitignore + dep-dir skip), compiled globs (gobwas),
// brace groups, and ** recursion. Native — no shell required.
func toolGlob(args toolArgs) string {
	pattern := strings.TrimSpace(args.Pattern)
	if pattern == "" {
		return "Error: pattern is required."
	}
	root, err := toolSearchRoot(args)
	if err != nil {
		return "Error: " + err.Error()
	}
	limit := args.MaxResults
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	matches, err := globWalk(root, pattern, limit)
	if err != nil {
		return "Error: " + err.Error()
	}
	return formatGlobMatches(matches, limit)
}

func toolListDir(args toolArgs) string {
	abs, err := toolSearchRoot(args)
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
	root, err := toolSearchRoot(args)
	if err != nil {
		return "Error: " + err.Error()
	}
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
	abs, err := resolvePath(args.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	if mustNotExist {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "Error: " + err.Error()
		}
		f, openErr := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(openErr) {
			return fmt.Sprintf("Error: %q already exists; use write_file to overwrite.", args.Path)
		}
		if openErr != nil {
			return "Error: " + openErr.Error()
		}
		if _, writeErr := f.WriteString(args.Content); writeErr != nil {
			_ = f.Close()
			_ = os.Remove(abs)
			return "Error: " + writeErr.Error()
		}
		if closeErr := f.Close(); closeErr != nil {
			_ = os.Remove(abs)
			return "Error: " + closeErr.Error()
		}
		return fmt.Sprintf("Wrote %d bytes to %s.", len(args.Content), abs)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "Error: " + err.Error()
	}
	if err := os.WriteFile(abs, []byte(args.Content), 0o644); err != nil {
		return "Error: " + err.Error()
	}
	return fmt.Sprintf("Wrote %d bytes to %s.", len(args.Content), abs)
}

// toolExec runs an arbitrary shell command anywhere on the host. SapaLOQ is
// unrestricted by design: the working directory defaults to the process CWD and
// honors an explicit cwd argument (any path). Output is byte-capped and a
// timeout guards runaway commands.
func toolExec(ctx context.Context, args toolArgs) string {
	return (&Orchestrator{}).toolExec(ctx, args)
}

func (o *Orchestrator) toolExec(ctx context.Context, args toolArgs) string {
	args = o.resolveActorArgs(ctx, args)
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
	res := runShellCaptured(ctx, cmd, args.Cwd, time.Duration(timeout)*time.Second)
	if res.FinalCWD != "" {
		o.persistActorCWD(actorRunID(ctx), res.FinalCWD)
	}
	text := res.Output
	if res.TimedOut {
		return fmt.Sprintf("Command timed out after %ds (process group killed). If this command is long-running (a server, watcher, or anything started with '&'), rerun it with wait_for_output:false so it runs in the background, then collect it via wait {mode:'tool', job_id:...}.\n%s", timeout, text)
	}
	if res.Cancelled {
		return fmt.Sprintf("Command cancelled by host.\n%s", text)
	}
	if res.Err != nil {
		return fmt.Sprintf("Command exited with error: %v\n%s", res.Err, text)
	}
	if strings.TrimSpace(text) == "" {
		return "(command produced no output)"
	}
	return enrichToolResultWithArtifactFingerprint("exec", cmd, text)
}

func splitExecCWD(output, marker string) (string, string) {
	index := strings.LastIndex(output, "\n"+marker)
	if index < 0 {
		return output, ""
	}
	tail := output[index+1+len(marker):]
	lineEnd := strings.IndexByte(tail, '\n')
	if lineEnd >= 0 {
		tail = tail[:lineEnd]
	}
	return strings.TrimSuffix(output[:index], "\n"), strings.TrimSpace(tail)
}
