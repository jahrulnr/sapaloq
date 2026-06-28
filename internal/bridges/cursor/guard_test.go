package cursor

import (
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func TestBuildGuardContextDefaultModel(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	gc := schema.BuildGuardContext("cu/default", []string{"workspace_list_dir"}, nil)
	if !gc.ForceAgentMode {
		t.Fatal("expected forceAgentMode for default")
	}
	if gc.Instruction == "" {
		t.Fatal("expected instruction guard text")
	}
	if !gc.ApplySanitizer {
		t.Fatal("expected response sanitizer")
	}
}

func TestBuildGuardContextSkipsAgentSession(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	gc := schema.BuildGuardContext("default", []string{"run_terminal_cmd"}, nil)
	if gc.Instruction != "" {
		t.Fatalf("agent session should skip guard instruction, got %q", gc.Instruction)
	}
}

func TestSanitizeToolSchemaLeak(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	gc := schema.BuildGuardContext("default", nil, nil)
	leak := "Daftar schema tools: glob_file_search **parameters** read_file **parameters**"
	got := schema.SanitizeToolSchemaLeakContent(leak, gc)
	if got != defaultGuardSafeReply {
		t.Fatalf("sanitize = %q", got)
	}
}

func TestShouldSuppressKimiToolChunk(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !ShouldSuppressKimiToolStreamChunk("<|tool_calls_begin|>", schema.KimiTokens()) {
		t.Fatal("expected suppress")
	}
	if ShouldSuppressKimiToolStreamChunk("hello", schema.KimiTokens()) {
		t.Fatal("plain text should not suppress")
	}
}

func TestBuildGuardContextSkipsAgentToolsInPrompt(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	msgs := []bridge.Message{{
		Role:    "user",
		Content: "[System Instructions]\n# run_terminal_cmd\nUse this tool to execute commands.",
	}}
	gc := schema.BuildGuardContext("default", nil, msgs)
	if gc.Instruction != "" {
		t.Fatalf("prompt-declared agent session should skip guard, got %q", gc.Instruction)
	}
}

func TestVisibleContentFromThinking(t *testing.T) {
	raw := "reasoning here</think>Hello from composer"
	if got := VisibleContentFromThinking(raw); got != "Hello from composer" {
		t.Fatalf("visible = %q", got)
	}
}

func TestShouldPromoteThinkingToContent(t *testing.T) {
	if !ShouldPromoteThinkingToContent("default") {
		t.Fatal("default should promote")
	}
	if ShouldPromoteThinkingToContent("claude-4.5-sonnet") {
		t.Fatal("named model should not promote")
	}
}
