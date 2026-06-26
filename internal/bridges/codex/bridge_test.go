package codex

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// TestComposePrompt_FreshTurn verifies a fresh turn prepends a compact
// transcript (system + prior conversation) and ends with the latest user turn.
func TestComposePrompt_FreshTurn(t *testing.T) {
	req := bridge.Request{
		Messages: []bridge.Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
			{Role: "user", Content: "what is 2+2?"},
		},
	}
	got := composePrompt(req, false)
	for _, want := range []string{"[system]", "be terse", "[conversation]", "user: hi", "assistant: hello", "[user]", "what is 2+2?"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fresh prompt missing %q\n---\n%s", want, got)
		}
	}
	// The latest user turn must come last (it is the explicit prompt), not be
	// duplicated inside the [conversation] block.
	if strings.Count(got, "what is 2+2?") != 1 {
		t.Fatalf("latest user turn duplicated:\n%s", got)
	}
}

// TestComposePrompt_ResumeTurn verifies a resume turn sends ONLY the latest
// user turn (Codex already holds the history), keeping prompt size bounded.
func TestComposePrompt_ResumeTurn(t *testing.T) {
	req := bridge.Request{
		Messages: []bridge.Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "earlier question"},
			{Role: "assistant", Content: "earlier answer"},
			{Role: "user", Content: "the new question"},
		},
	}
	got := composePrompt(req, true)
	if got != "the new question" {
		t.Fatalf("resume prompt = %q, want only the latest user turn", got)
	}
	if strings.Contains(got, "[system]") || strings.Contains(got, "earlier") {
		t.Fatalf("resume prompt leaked history:\n%s", got)
	}
}

// TestComposePrompt_ToolTurnIsLatest treats a fresh tool observation as the
// latest input (matching cursor's lastUserMessage semantics).
func TestComposePrompt_ToolTurnIsLatest(t *testing.T) {
	req := bridge.Request{
		Messages: []bridge.Message{
			{Role: "user", Content: "run it"},
			{Role: "assistant", Content: "calling tool"},
			{Role: "tool", Content: "tool output here"},
		},
	}
	if got := composePrompt(req, true); got != "tool output here" {
		t.Fatalf("resume tool turn = %q, want the tool observation", got)
	}
}

// TestSafeReasoning enforces the hard contract constraint (CONTRACT §7): never
// pass model_reasoning_effort=minimal while tools are active; downgrade to low.
func TestSafeReasoning(t *testing.T) {
	cases := []struct {
		effort string
		want   string
	}{
		{"", ""},
		{"low", "low"},
		{"medium", "medium"},
		{"high", "high"},
		{"HIGH", "high"},
		{"minimal", "low"}, // downgraded — never 400 the turn
		{"  minimal  ", "low"},
	}
	for _, tc := range cases {
		b := &Bridge{entry: config.LLMBridge{ReasoningEffort: tc.effort}}
		if got := b.safeReasoning(bridge.Request{}); got != tc.want {
			t.Errorf("safeReasoning(%q) = %q, want %q", tc.effort, got, tc.want)
		}
	}
}

// TestSafeReasoningNeverMinimalInArgv is the end-to-end guard: building the
// argv for a minimal-effort entry must never emit `model_reasoning_effort=minimal`.
func TestSafeReasoningNeverMinimalInArgv(t *testing.T) {
	b := &Bridge{entry: config.LLMBridge{ReasoningEffort: "minimal"}}
	argv := buildArgv(runOptions{sandbox: "workspace-write", reasoning: b.safeReasoning(bridge.Request{})})
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "minimal") {
		t.Fatalf("argv must never carry minimal reasoning effort: %s", joined)
	}
	if !strings.Contains(joined, "model_reasoning_effort=low") {
		t.Fatalf("expected downgraded effort=low in argv: %s", joined)
	}
}

// TestNewResolvesBinaryFromEnv confirms New honours SAPALOQ_CODEX_BINARY (so we
// never depend on a real codex on PATH) and resolves the per-turn timeout, the
// default sandbox, and a workspace cwd from the runtime dirs.
func TestNewResolvesBinaryFromEnv(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(envBinary, "/usr/bin/true") // a real, harmless binary
	t.Setenv(envSandbox, "")             // exercise the default
	t.Setenv(envCwd, "")

	entry := config.LLMBridge{Driver: "codex-bridge", RequestTimeoutSec: 321}
	runtime := config.RuntimeConfig{DataDir: dataDir}
	b, err := New(entry, runtime)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if b.binary != "/usr/bin/true" {
		t.Errorf("binary = %q, want the env override", b.binary)
	}
	if b.sandbox != defaultSandbox {
		t.Errorf("sandbox = %q, want default %q", b.sandbox, defaultSandbox)
	}
	if b.cwd != filepath.Join(dataDir, "workspace") {
		t.Errorf("cwd = %q, want the workspace dir", b.cwd)
	}
	if b.timeout != 321*time.Second {
		t.Errorf("timeout = %v, want 321s", b.timeout)
	}
	if b.ID() != "codex-bridge" {
		t.Errorf("ID = %q", b.ID())
	}
	// The thread store file lives under the vault dir.
	if want := filepath.Join(dataDir, "vault", "codex-threads.jsonl"); b.store.path != want {
		t.Errorf("store path = %q, want %q", b.store.path, want)
	}
}

// TestNewMissingBinaryFails verifies a missing codex binary surfaces an
// actionable error rather than a panic.
func TestNewMissingBinaryFails(t *testing.T) {
	t.Setenv(envBinary, filepath.Join(t.TempDir(), "definitely-not-a-real-codex"))
	_, err := New(config.LLMBridge{}, config.RuntimeConfig{DataDir: t.TempDir()})
	// LookPath is bypassed because envBinary is set, so New succeeds with the
	// (nonexistent) path — the failure surfaces later at spawn. To exercise the
	// resolve path we clear the env and point PATH at an empty dir.
	if err != nil {
		return
	}
	t.Setenv(envBinary, "")
	t.Setenv("PATH", t.TempDir())
	if _, err := New(config.LLMBridge{}, config.RuntimeConfig{DataDir: t.TempDir()}); err == nil {
		t.Fatal("expected an error when codex cannot be resolved on PATH")
	}
}

// TestChildEnvInjectsAPIKey verifies CredentialsEnv is injected as
// OPENAI_API_KEY for API-key auth, without clobbering an existing one.
func TestChildEnvInjectsAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("MY_CODEX_KEY", "sk-test-123")
	b := &Bridge{entry: config.LLMBridge{CredentialsEnv: "MY_CODEX_KEY"}}
	env := b.childEnv()
	var found bool
	for _, kv := range env {
		if kv == "OPENAI_API_KEY=sk-test-123" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected OPENAI_API_KEY injected from CredentialsEnv")
	}

	// An existing OPENAI_API_KEY must not be clobbered.
	t.Setenv("OPENAI_API_KEY", "sk-existing")
	for _, kv := range b.childEnv() {
		if kv == "OPENAI_API_KEY=sk-test-123" {
			t.Fatalf("existing OPENAI_API_KEY was clobbered")
		}
	}
}

// TestAuthOKFromEnvKey confirms Caps().LiveAPI is true when an API key is in the
// environment (no codex CLI call needed).
func TestAuthOKFromEnvKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-live")
	b := &Bridge{entry: config.LLMBridge{}, binary: "/nonexistent"}
	if !b.Caps().LiveAPI {
		t.Fatal("expected LiveAPI=true with OPENAI_API_KEY set")
	}
	if !b.Caps().Thinking || !b.Caps().Tools {
		t.Fatal("Thinking and Tools must always be true for codex-bridge")
	}
}
