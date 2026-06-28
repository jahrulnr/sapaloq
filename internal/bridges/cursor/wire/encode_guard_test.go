package wire

import (
	"strings"
	"testing"
)

func TestBuildChatBodyWithInstructionAndAgentMode(t *testing.T) {
	body := BuildChatBodyWithOptions(
		[]ChatMessage{{Role: "user", Content: "hi"}},
		"default",
		ChatEncodeOptions{
			ForceAgentMode: true,
			Instruction:    "OpenAI bridge: no tools[] declared.",
			Tools: []MCPToolDecl{{
				Name:           "workspace_list_dir",
				Description:    "list dir",
				ParametersJSON: `{"type":"object","additionalProperties":true}`,
			}},
			ReasoningEffort: "medium",
		},
	)
	if len(body) < 20 {
		t.Fatalf("body too short: %d", len(body))
	}
	raw := string(body)
	if !strings.Contains(raw, "OpenAI bridge") {
		t.Fatal("instruction text not present in body")
	}
	if !strings.Contains(raw, "workspace_list_dir") {
		t.Fatal("tool name not present in body")
	}
}
