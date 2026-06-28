package provider

import (
	"encoding/json"
	"testing"
)

func TestBuildOpenAIToolsIncludesDescription(t *testing.T) {
	const name = "test_tool_desc_wire"
	const desc = "Does the thing when called."
	RegisterTool(name, json.RawMessage(`{"type":"object","properties":{}}`), desc)
	t.Cleanup(func() {
		toolSchemaMu.Lock()
		delete(toolRegistry, name)
		toolSchemaMu.Unlock()
	})

	tools := buildOpenAITools([]string{name})
	if len(tools) != 1 {
		t.Fatalf("len = %d", len(tools))
	}
	if tools[0].Function.Description != desc {
		t.Fatalf("description = %q, want %q", tools[0].Function.Description, desc)
	}
}

func TestBuildClaudeToolsIncludesDescription(t *testing.T) {
	const name = "test_tool_desc_claude"
	const desc = "Claude sees this."
	RegisterTool(name, json.RawMessage(`{"type":"object","properties":{}}`), desc)
	t.Cleanup(func() {
		toolSchemaMu.Lock()
		delete(toolRegistry, name)
		toolSchemaMu.Unlock()
	})

	tools := buildClaudeTools([]string{name})
	if len(tools) != 1 {
		t.Fatalf("len = %d", len(tools))
	}
	if tools[0].Description != desc {
		t.Fatalf("description = %q, want %q", tools[0].Description, desc)
	}
}
