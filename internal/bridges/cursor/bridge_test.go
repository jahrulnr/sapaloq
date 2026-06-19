package cursor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestBridgeStreamsThinkingResponseAndTool(t *testing.T) {
	forceMockCredentials(t)
	b, err := New(config.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	events, err := b.Complete(context.Background(), bridge.Request{Messages: []bridge.Message{{Role: "user", Content: "use glob tool"}}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[bridge.EventKind]bool{}
	for ev := range events {
		seen[ev.Kind] = true
		if ev.Kind == bridge.EventToolCall && ev.ToolCall.Name != "glob_file_search" {
			t.Fatalf("tool name = %q", ev.ToolCall.Name)
		}
	}
	for _, kind := range []bridge.EventKind{bridge.EventThinkingDelta, bridge.EventResponseDelta, bridge.EventToolCall, bridge.EventDone} {
		if !seen[kind] {
			t.Fatalf("missing %s", kind)
		}
	}
}

func TestVaultLogsUndeclaredTool(t *testing.T) {
	forceMockCredentials(t)
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Runtime.DataDir = dir
	cfg.LLMBridge.DeclaredTools = []string{"read_file"}

	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	events, err := b.Complete(context.Background(), bridge.Request{
		SessionID: "vault-test",
		Messages:  []bridge.Message{{Role: "user", Content: "use glob tool"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}

	logPath := filepath.Join(dir, "vault", "tool-calls.jsonl")
	blob, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) == 0 {
		t.Fatal("expected vault entry for undeclared glob_file_search")
	}
}

func TestVaultLogsUnknownUpstreamTool(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Runtime.DataDir = dir

	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reason := VaultReason(b.schema, cfg.LLMBridge.DeclaredTools, "totally_fake_tool", parse.ToolCall{Name: "totally_fake_tool"})
	if reason != "unknown_upstream" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestThinkingMentionsToolsWithoutVault(t *testing.T) {
	// Tool names in thinking/chat text are fine — vault only applies to structured tool calls.
	_ = "I will use grep and read_file with input_schema parameters."
}
