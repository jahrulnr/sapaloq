package codex

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/debug"
)

// runOptions configures a single spawn-per-turn invocation of the codex CLI.
// It is built from the bridge config + Request by runTurn; the prompt is fed
// via stdin (contract §1.1) and never concatenated into the argv.
type runOptions struct {
	binary         string
	prompt         string
	cwd            string
	model          string
	sandbox        string
	reasoning      string // empty => omit -c model_reasoning_effort
	images         []string
	resumeThreadID string // non-empty => `exec resume <id>`
	env            []string
	rawSink        io.Writer // optional: tee raw stdout JSONL (fixture capture)
}

// buildArgv assembles a typed argv (never a shell string) so user content is
// never concatenated into a command line — there is no shell-injection surface.
// The prompt always travels via stdin (arg "-").
//
// Contract invariants baked in here (CODEX_CLI_CONTRACT.md):
//   - `--json` belongs to `exec`, so on a resume it must precede `resume` (§2).
//   - `codex exec` rejects `-a/--ask-for-approval` (exit 2); never pass it (§9).
//   - `exec` is non-interactive by default; sandbox is the only safety knob (§9).
//   - The `resume` subcommand has its OWN, narrower flag set: it REJECTS
//     `-s/--sandbox`, `-C/--cd`, and `--add-dir` (exit 2: "unexpected argument",
//     verified codex v0.141.0). These are fresh-`exec`-only knobs. On resume the
//     sandbox and working directory are inherited from the original session, so
//     they must NOT be restated — see §2.
//
// We therefore split the argv into a fresh-`exec` path (carries -s/-C) and a
// `resume` path (omits them, carries only resume-valid flags).
func buildArgv(opts runOptions) []string {
	argv := []string{"exec", "--json"}
	if opts.resumeThreadID != "" {
		// RESUME path. `resume` inherits sandbox + cwd from the persisted
		// session, so -s/-C/--add-dir are both unnecessary and rejected (exit 2,
		// codex v0.141.0). Only resume-valid flags are appended.
		argv = append(argv, "resume", opts.resumeThreadID, "--skip-git-repo-check")
		argv = appendCommonFlags(argv, opts)
		argv = append(argv, "-") // prompt via stdin
		return argv
	}

	// FRESH exec path. The sandbox (-s) and working root (-C) are valid and
	// required here to establish the session's posture.
	argv = append(argv, "--skip-git-repo-check", "-s", opts.sandbox)
	if opts.cwd != "" {
		argv = append(argv, "-C", opts.cwd)
	}
	argv = appendCommonFlags(argv, opts)
	argv = append(argv, "-") // prompt via stdin
	return argv
}

// appendCommonFlags appends the flags accepted by BOTH `codex exec` and
// `codex exec resume` (verified codex v0.141.0): -m/--model, -c key=value, and
// -i/--image. Keeping them in one place guarantees the fresh and resume paths
// stay in sync and that no fresh-only flag leaks into resume.
func appendCommonFlags(argv []string, opts runOptions) []string {
	if opts.model != "" {
		argv = append(argv, "-m", opts.model)
	}
	if opts.reasoning != "" {
		argv = append(argv, "-c", "model_reasoning_effort="+opts.reasoning)
	}
	for _, img := range opts.images {
		argv = append(argv, "-i", img)
	}
	return argv
}

// runResult reports what the turn produced that the caller needs after the
// stream drains: the captured thread_id (for resume bookkeeping), whether the
// process started at all, and whether a resume target was rejected so the
// caller can self-heal with a fresh turn.
type runResult struct {
	threadID    string
	started     bool
	resumeError bool
}

// runCodex spawns the codex CLI, scans stdout as JSONL into bridge events on
// out, and drains stderr into the debug log only (contract §5 — stderr is noise
// and must never reach the JSONL scanner). It blocks until the stream drains or
// ctx is cancelled, then reaps the process. The whole process group is killed
// on cancellation so child shells spawned by command_execution die too (§10).
func runCodex(ctx context.Context, opts runOptions, sessionID string, out chan<- bridge.StreamEvent) (runResult, error) {
	argv := buildArgv(opts)
	debug.Debugf("codex-bridge: spawn %s %s (session=%s resume=%q)", opts.binary, strings.Join(argv, " "), sessionID, opts.resumeThreadID)

	cmd := exec.CommandContext(ctx, opts.binary, argv...)
	cmd.Stdin = strings.NewReader(opts.prompt)
	if len(opts.env) > 0 {
		cmd.Env = opts.env
	}
	setProcessGroup(cmd) // platform-specific: kill the whole group on cancel.

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return runResult{}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return runResult{}, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return runResult{}, fmt.Errorf("start codex: %w", err)
	}

	res := runResult{started: true}

	// Drain stderr in its own goroutine. We keep a bounded tail so an abnormal
	// exit can report a useful cause, and forward everything to the debug log.
	stderrTail := &boundedTail{max: 16}
	var stderrWG sync.WaitGroup
	stderrWG.Add(1)
	go func() {
		defer stderrWG.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			stderrTail.add(line)
			// A resume against a missing/pruned session shows up on stderr; flag
			// it so the caller can self-heal with a fresh exec (design §7).
			if opts.resumeThreadID != "" && looksLikeResumeFailure(line) {
				res.resumeError = true
			}
			debug.Verbosef("codex-bridge[stderr]: %s", line)
		}
	}()

	// abnormal supplies the process exit cause for the no-terminal-event case.
	// It is only consulted after Scan drains stdout (EOF), so the process has
	// effectively finished writing; calling Wait here is safe.
	abnormal := func() string {
		waitErr := cmd.Wait()
		stderrWG.Wait()
		tail := stderrTail.joined()
		switch {
		case waitErr != nil && tail != "":
			return fmt.Sprintf("exit: %v; stderr: %s", waitErr, tail)
		case waitErr != nil:
			return fmt.Sprintf("exit: %v", waitErr)
		case tail != "":
			return fmt.Sprintf("stderr: %s", tail)
		default:
			return ""
		}
	}

	var src io.Reader = stdout
	if opts.rawSink != nil {
		src = newLineTee(stdout, opts.rawSink)
	}

	scan, _ := scanStream(ctx, sessionID, src, out)
	res.threadID = scan.threadID
	// Emit exactly one terminal event from the event stream (contract §4). The
	// abnormal-exit branch is the only one that consults the process exit, so it
	// is computed lazily there (abnormal calls Wait).
	finalizeTerminal(ctx, sessionID, scan, out, abnormal)

	// On the success/failure paths Scan does not call abnormal, so the process
	// may be unreaped. Reap it to avoid a zombie; Wait twice is harmless (the
	// second returns an error that we ignore).
	_ = cmd.Wait()
	stderrWG.Wait()
	return res, nil
}

// looksLikeResumeFailure heuristically detects a stderr line indicating the
// resume target session could not be loaded. Best-effort: a false negative just
// means we surface the turn's own error event instead of self-healing.
func looksLikeResumeFailure(line string) bool {
	l := strings.ToLower(line)
	return (strings.Contains(l, "session") || strings.Contains(l, "thread") || strings.Contains(l, "resume")) &&
		(strings.Contains(l, "not found") || strings.Contains(l, "no such") ||
			strings.Contains(l, "missing") || strings.Contains(l, "failed to load") ||
			strings.Contains(l, "does not exist"))
}

// lineTee copies each newline-terminated line read through it to sink so the
// raw JSONL can be captured for golden fixtures without a second read of the
// pipe. It preserves the byte stream exactly for the downstream scanner.
type lineTee struct {
	sc   *bufio.Scanner
	sink io.Writer
	buf  []byte
}

func newLineTee(r io.Reader, sink io.Writer) *lineTee {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	return &lineTee{sc: sc, sink: sink}
}

func (t *lineTee) Read(p []byte) (int, error) {
	if len(t.buf) == 0 {
		if !t.sc.Scan() {
			if err := t.sc.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		line := append(t.sc.Bytes(), '\n')
		_, _ = io.WriteString(t.sink, string(line))
		t.buf = line
	}
	n := copy(p, t.buf)
	t.buf = t.buf[n:]
	return n, nil
}

// boundedTail keeps the last max lines, used to attach a short stderr context
// to an abnormal-exit error without buffering unbounded output.
type boundedTail struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func (b *boundedTail) add(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
}

func (b *boundedTail) joined() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.lines, " | ")
}

// resolveBinary resolves the codex executable via PATH. We never hardcode the
// release symlink target because it changes on `codex update`.
func resolveBinary() (string, error) {
	return exec.LookPath("codex")
}

// decodeImagesToTempFiles writes each inline image to a temp file and returns
// the paths plus a cleanup func. Codex takes images as files via -i (§6).
func decodeImagesToTempFiles(images []bridge.Image) (paths []string, cleanup func(), err error) {
	var files []string
	cleanup = func() {
		for _, p := range files {
			_ = os.Remove(p)
		}
	}
	for i, img := range images {
		data, ext := imageBytes(img)
		if len(data) == 0 {
			continue
		}
		f, ferr := os.CreateTemp("", fmt.Sprintf("codex-img-%d-*%s", i, ext))
		if ferr != nil {
			cleanup()
			return nil, func() {}, ferr
		}
		if _, werr := f.Write(data); werr != nil {
			_ = f.Close()
			cleanup()
			return nil, func() {}, werr
		}
		_ = f.Close()
		files = append(files, f.Name())
	}
	return files, cleanup, nil
}

// imageBytes extracts the raw image bytes and a file extension from a
// bridge.Image. It accepts either pre-decoded Data (+MimeType) or a
// data:<mime>;base64,<payload> URI. Returns nil bytes when neither is usable.
func imageBytes(img bridge.Image) (data []byte, ext string) {
	if len(img.Data) > 0 {
		return img.Data, extForMime(img.MimeType)
	}
	uri := strings.TrimSpace(img.DataURI)
	if uri == "" {
		return nil, ""
	}
	// data:[<mediatype>][;base64],<data>
	if !strings.HasPrefix(uri, "data:") {
		return nil, ""
	}
	comma := strings.IndexByte(uri, ',')
	if comma < 0 {
		return nil, ""
	}
	meta, payload := uri[len("data:"):comma], uri[comma+1:]
	mime := meta
	if i := strings.IndexByte(meta, ';'); i >= 0 {
		mime = meta[:i]
	}
	if strings.Contains(meta, "base64") {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			debug.Debugf("codex-bridge: image data-uri base64 decode failed: %v", err)
			return nil, ""
		}
		return decoded, extForMime(mime)
	}
	// Non-base64 data URIs are URL-encoded text; codex expects real image files
	// so we only support base64 image payloads here.
	return nil, ""
}

// extForMime maps a MIME type to a sensible file extension for the temp file.
// Codex infers the format from the file, so a wrong extension is harmless, but
// a matching one keeps the temp files self-describing.
func extForMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}
