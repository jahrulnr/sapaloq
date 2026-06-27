package codex

import (
	"os"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// mkImage builds a bridge.Image from a URI for the image-decode tests.
func mkImage(uri string) bridge.Image { return bridge.Image{DataURI: uri} }

// TestBuildArgvFreshTurn pins the contract invariants for a new turn
// (CODEX_CLI_CONTRACT.md §1, §9): `exec --json`, the safe sandbox, the
// skip-git-repo-check flag, and the trailing `-` (prompt via stdin). Crucially
// it must NOT carry `-a/--ask-for-approval`, which `codex exec` rejects (exit 2).
func TestBuildArgvFreshTurn(t *testing.T) {
	argv := buildArgv(runOptions{sandbox: "workspace-write", cwd: "/work", model: "gpt-5.5"})
	joined := strings.Join(argv, " ")

	if argv[0] != "exec" || argv[1] != "--json" {
		t.Fatalf("argv must start with `exec --json`, got %v", argv)
	}
	if !strings.Contains(joined, "--skip-git-repo-check") {
		t.Errorf("missing --skip-git-repo-check: %v", argv)
	}
	if !strings.Contains(joined, "-s workspace-write") {
		t.Errorf("missing sandbox flag: %v", argv)
	}
	if !strings.Contains(joined, "-C /work") {
		t.Errorf("missing cwd flag: %v", argv)
	}
	if !strings.Contains(joined, "-m gpt-5.5") {
		t.Errorf("missing model flag: %v", argv)
	}
	// The prompt always travels via stdin: the last arg must be "-".
	if argv[len(argv)-1] != "-" {
		t.Errorf("last arg must be `-` (stdin prompt), got %q", argv[len(argv)-1])
	}
	// VERIFIED contract: `-a`/`--ask-for-approval` is rejected by `codex exec`.
	for _, a := range argv {
		if a == "-a" || a == "--ask-for-approval" {
			t.Fatalf("argv must never include an approval flag (codex exec rejects it): %v", argv)
		}
	}
	// `resume` must be absent on a fresh turn.
	if strings.Contains(joined, "resume") {
		t.Errorf("fresh turn must not contain resume: %v", argv)
	}
}

// TestBuildArgvResume pins the resume-path contract (CONTRACT §2, verified codex
// v0.141.0):
//   - `--json` belongs to `exec`, so it must precede `resume <id>` (reversed
//     order exits 2);
//   - the `resume` subcommand REJECTS the fresh-only knobs `-s/--sandbox`,
//     `-C/--cd`, and `--add-dir` (exit 2 "unexpected argument"); they are
//     inherited from the original session and must never leak into resume;
//   - resume-valid flags we still pass (`--skip-git-repo-check`,
//     `-m/--model`, `-c model_reasoning_effort=`) are present;
//   - the prompt still travels via stdin (trailing `-`).
func TestBuildArgvResume(t *testing.T) {
	// A cwd is set on purpose: it must NOT surface as `-C` on the resume path.
	argv := buildArgv(runOptions{
		sandbox:        "read-only",
		cwd:            "/work",
		resumeThreadID: "019f-tid",
		model:          "gpt-5.5",
		reasoning:      "low",
	})

	jsonIdx, resumeIdx := -1, -1
	for i, a := range argv {
		switch a {
		case "--json":
			jsonIdx = i
		case "resume":
			resumeIdx = i
		}
	}
	if jsonIdx < 0 || resumeIdx < 0 {
		t.Fatalf("expected both --json and resume in argv: %v", argv)
	}
	if jsonIdx > resumeIdx {
		t.Fatalf("--json must precede resume (else exit 2): %v", argv)
	}
	if argv[resumeIdx+1] != "019f-tid" {
		t.Errorf("resume thread_id misplaced: %v", argv)
	}

	// The fresh-only flags must NEVER appear on a resume — codex v0.141.0 rejects
	// each with exit 2 "unexpected argument". This is the regression guard for
	// the resume bug.
	for _, banned := range []string{"-s", "--sandbox", "-C", "--cd", "--add-dir"} {
		for _, a := range argv {
			if a == banned {
				t.Fatalf("resume argv must never contain fresh-only flag %q: %v", banned, argv)
			}
		}
	}
	// The session posture must not leak as a value either (e.g. a bare
	// "read-only" / "/work" left dangling without its flag).
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "read-only") {
		t.Errorf("resume argv must not restate the sandbox value: %v", argv)
	}
	if strings.Contains(joined, "/work") {
		t.Errorf("resume argv must not restate the cwd value: %v", argv)
	}

	// Resume-valid flags we still rely on must be present.
	if !strings.Contains(joined, "--skip-git-repo-check") {
		t.Errorf("resume must keep --skip-git-repo-check: %v", argv)
	}
	if !strings.Contains(joined, "-m gpt-5.5") {
		t.Errorf("resume must keep the model flag: %v", argv)
	}
	if !strings.Contains(joined, "-c model_reasoning_effort=low") {
		t.Errorf("missing reasoning override: %v", argv)
	}
	// Prompt via stdin: the last arg must be "-".
	if argv[len(argv)-1] != "-" {
		t.Errorf("resume last arg must be `-` (stdin prompt), got %q", argv[len(argv)-1])
	}
}

// TestBuildArgvResumeImages verifies resume still carries `-i` image pairs (a
// resume-valid flag) without smuggling any fresh-only flag.
func TestBuildArgvResumeImages(t *testing.T) {
	argv := buildArgv(runOptions{
		sandbox:        "workspace-write",
		cwd:            "/work",
		resumeThreadID: "tid-1",
		images:         []string{"/tmp/a.png"},
	})
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-i /tmp/a.png") {
		t.Errorf("resume must keep image flag: %v", argv)
	}
	for _, a := range argv {
		if a == "-s" || a == "-C" || a == "--add-dir" {
			t.Fatalf("resume argv leaked fresh-only flag: %v", argv)
		}
	}
}

// TestBuildArgvImages: each image becomes a `-i <file>` pair.
func TestBuildArgvImages(t *testing.T) {
	argv := buildArgv(runOptions{sandbox: "workspace-write", images: []string{"/tmp/a.png", "/tmp/b.png"}})
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-i /tmp/a.png") || !strings.Contains(joined, "-i /tmp/b.png") {
		t.Errorf("expected both image flags: %v", argv)
	}
	if strings.Count(joined, "-i ") != 2 {
		t.Errorf("expected exactly two image flags: %v", argv)
	}
}

// TestImageBytesDataURI exercises the base64 data-URI decode path used to
// materialize Codex `-i` image files.
func TestImageBytesDataURI(t *testing.T) {
	// "hi" base64-encoded is "aGk=".
	data, ext := imageBytes(mkImage("data:image/png;base64,aGk="))
	if string(data) != "hi" {
		t.Fatalf("decoded data = %q, want %q", data, "hi")
	}
	if ext != ".png" {
		t.Fatalf("ext = %q, want .png", ext)
	}

	// A non-data URI yields nothing (Codex needs a real file).
	if d, _ := imageBytes(mkImage("https://example.com/x.png")); d != nil {
		t.Fatalf("non-data URI should decode to nil, got %q", d)
	}
	// A non-base64 data URI is unsupported (codex needs real image files).
	if d, _ := imageBytes(mkImage("data:text/plain,hello")); d != nil {
		t.Fatalf("non-base64 data URI should decode to nil, got %q", d)
	}
}

// TestLooksLikeResumeFailure spot-checks the heuristic that flags a missing
// resume target on stderr so runTurn can self-heal.
func TestLooksLikeResumeFailure(t *testing.T) {
	yes := []string{
		"ERROR session 019f not found",
		"failed to load thread file",
		"resume target does not exist",
	}
	for _, l := range yes {
		if !looksLikeResumeFailure(l) {
			t.Errorf("expected resume-failure for %q", l)
		}
	}
	no := []string{
		"INFO codex_core starting up",
		"Reading additional input from stdin...",
	}
	for _, l := range no {
		if looksLikeResumeFailure(l) {
			t.Errorf("unexpected resume-failure for %q", l)
		}
	}
}

// TestDecodeImagesTempFilesCleanup confirms a base64 data-URI image is written
// to a temp file and the cleanup func removes it afterwards.
func TestDecodeImagesTempFilesCleanup(t *testing.T) {
	imgs := []bridge.Image{{DataURI: "data:image/png;base64,aGk="}} // "hi"
	paths, cleanup, err := decodeImagesToTempFiles(imgs)
	if err != nil {
		t.Fatalf("decodeImagesToTempFiles: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected 1 temp file, got %d (%v)", len(paths), paths)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("read temp image: %v", err)
	}
	if string(data) != "hi" {
		t.Fatalf("temp image content = %q, want %q", data, "hi")
	}
	cleanup()
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("temp image not cleaned up: %v", err)
	}
}
