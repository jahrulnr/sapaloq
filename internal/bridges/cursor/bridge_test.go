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

// defaultTestEntry returns the cursor entry from DefaultConfig + the
// runtime config. Tests that need a custom entry build their own.
func defaultTestEntry() (config.LLMBridge, config.RuntimeConfig) {
	cfg := config.DefaultConfig()
	entry, err := cfg.LLMBridge.ActiveProvider()
	if err != nil {
		panic(err)
	}
	return entry, cfg.Runtime
}

func TestBridgeStreamsThinkingResponseAndTool(t *testing.T) {
	forceMockCredentials(t)
	entry, runtime := defaultTestEntry()
	b, err := New(entry, runtime)
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

func TestBridgeMockHonorsAutopilotStop(t *testing.T) {
	forceMockCredentials(t)
	entry, runtime := defaultTestEntry()
	b, err := New(entry, runtime)
	if err != nil {
		t.Fatal(err)
	}
	msg := "<sapaloq:autopilot>\nInvoke `sapaloq_stop` silently now.\n</sapaloq:autopilot>"
	events, err := b.Complete(context.Background(), bridge.Request{
		Messages: []bridge.Message{{Role: "user", Content: msg}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var toolName string
	var responseText string
	for ev := range events {
		if ev.Kind == bridge.EventToolCall && ev.ToolCall != nil {
			toolName = ev.ToolCall.Name
		}
		if ev.Kind == bridge.EventResponseDelta {
			responseText += ev.Delta
		}
	}
	if toolName != "sapaloq_stop" {
		t.Fatalf("tool = %q want sapaloq_stop", toolName)
	}
	if responseText != "" {
		t.Fatalf("autopilot mock should not emit response text, got %q", responseText)
	}
}

func TestVaultLogsUndeclaredTool(t *testing.T) {
	dir := t.TempDir()
	entry, _ := defaultTestEntry()
	entry.DeclaredTools = []string{"read_file"}

	b, err := New(entry, config.RuntimeConfig{DataDir: dir, BinaryName: "sapaloq-core"})
	if err != nil {
		t.Fatal(err)
	}
	call := parse.ToolCall{Name: "glob_file_search", Source: "kimi_inline"}
	b.tryEmitToolCall(context.Background(), nil, "vault-test", entry.DeclaredTools, call)

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
	entry, _ := defaultTestEntry()

	b, err := New(entry, config.RuntimeConfig{DataDir: dir, BinaryName: "sapaloq-core"})
	if err != nil {
		t.Fatal(err)
	}
	reason := VaultReason(b.schema, entry.DeclaredTools, "totally_fake_tool", parse.ToolCall{Name: "totally_fake_tool"})
	if reason != "unknown_upstream" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestThinkingMentionsToolsWithoutVault(t *testing.T) {
	// Tool names in thinking/chat text are fine - vault only applies to structured tool calls.
	_ = "I will use grep and read_file with input_schema parameters."
}
