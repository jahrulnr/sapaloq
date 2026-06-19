package provider

import (
	"encoding/json"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestAccumulatorOpenAI(t *testing.T) {
	acc := NewAccumulatorOpenAI("test")

	// First delta carries id + name + start of arguments.
	if !acc.Apply(OpenAIToolDelta{Index: 0, ID: "call_1", Type: "function", Function: struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	}{Name: "search", Arguments: `{"q":"hello`}}) {
		t.Fatal("first delta must report data")
	}

	// Second delta adds more arguments.
	if !acc.Apply(OpenAIToolDelta{Index: 0, Function: struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	}{Arguments: ` world"}`}}) {
		t.Fatal("second delta must report data")
	}

	// Delta with empty payload should be a no-op.
	if acc.Apply(OpenAIToolDelta{Index: 0}) {
		t.Fatal("empty delta must not report data")
	}

	calls := acc.Finish()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	got := calls[0]
	if got.ID != "call_1" {
		t.Errorf("id mismatch: %s", got.ID)
	}
	if got.Name != "search" {
		t.Errorf("name mismatch: %s", got.Name)
	}
	if string(got.Arguments) != `{"q":"hello world"}` {
		t.Errorf("arguments mismatch: %s", string(got.Arguments))
	}
	if got.Source != "test" {
		t.Errorf("source mismatch: %s", got.Source)
	}
}

func TestAccumulatorOpenAIMultipleIndices(t *testing.T) {
	acc := NewAccumulatorOpenAI("test")
	acc.Apply(OpenAIToolDelta{Index: 0, ID: "a", Function: struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	}{Name: "alpha", Arguments: "{}"}})
	acc.Apply(OpenAIToolDelta{Index: 1, ID: "b", Function: struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	}{Name: "beta", Arguments: "{}"}})
	calls := acc.Finish()
	if len(calls) != 2 {
		t.Fatalf("want 2, got %d", len(calls))
	}
	if calls[0].Name != "alpha" || calls[1].Name != "beta" {
		t.Errorf("order broken: %+v", calls)
	}
}

func TestAccumulatorOpenAIDropsUnnamed(t *testing.T) {
	acc := NewAccumulatorOpenAI("test")
	acc.Apply(OpenAIToolDelta{Index: 0, ID: "x", Function: struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	}{Arguments: "{}"}})
	if got := acc.Finish(); len(got) != 0 {
		t.Errorf("unnamed call must be dropped, got %+v", got)
	}
}

func TestAccumulatorKimiDelegatesToOpenAI(t *testing.T) {
	k := NewAccumulatorKimi("kimi")
	k.Apply(OpenAIToolDelta{Index: 0, ID: "k1", Function: struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	}{Name: "lookup", Arguments: "{}"}})
	calls := k.Finish()
	if len(calls) != 1 || calls[0].Name != "lookup" || calls[0].Source != "kimi" {
		t.Fatalf("kimi must mirror openai: %+v", calls)
	}
}

func TestAccumulatorClaudeToolUse(t *testing.T) {
	acc := NewAccumulatorClaude("claude")
	// Start a tool_use block at index 0.
	acc.Apply(ClaudeBlockEvent{
		Type:  "content_block_start",
		Index: 0,
		ContentBlock: &ClaudeBlockStart{
			Type: "tool_use",
			ID:   "tu_1",
			Name: "weather",
		},
	})
	// Deltas append input JSON.
	acc.Apply(ClaudeBlockEvent{
		Type:  "content_block_delta",
		Index: 0,
		Delta: &ClaudeBlockDelta{Type: "input_json_delta", PartialJSON: `{"city": "`},
	})
	acc.Apply(ClaudeBlockEvent{
		Type:  "content_block_delta",
		Index: 0,
		Delta: &ClaudeBlockDelta{Type: "input_json_delta", PartialJSON: `NYC"}`},
	})
	// Stop emits the payload.
	_, _, payload := acc.Apply(ClaudeBlockEvent{
		Type:  "content_block_stop",
		Index: 0,
	})
	if payload == "" {
		t.Fatal("content_block_stop must yield a payload")
	}
	tc, ok := DecodeToolCallPayload(payload)
	if !ok {
		t.Fatal("payload must decode")
	}
	if tc.Name != "weather" {
		t.Errorf("name: %s", tc.Name)
	}
	if string(tc.Arguments) != `{"city": "NYC"}` {
		t.Errorf("args: %s", string(tc.Arguments))
	}
}

func TestAccumulatorClaudeThinking(t *testing.T) {
	acc := NewAccumulatorClaude("claude")
	acc.Apply(ClaudeBlockEvent{
		Type:  "content_block_delta",
		Index: 1,
		Delta: &ClaudeBlockDelta{Type: "thinking_delta", Thinking: "let me think..."},
	})
	acc.Apply(ClaudeBlockEvent{
		Type:  "content_block_delta",
		Index: 2,
		Delta: &ClaudeBlockDelta{Type: "text_delta", Text: "answer"},
	})
}

func TestEncodeDecodeToolCallPayload(t *testing.T) {
	original := parse.ToolCall{ID: "x", Name: "noop", Arguments: json.RawMessage(`{"k":1}`)}
	encoded := EncodeToolCallPayload(original.Name, original.Arguments)
	tc, ok := DecodeToolCallPayload(encoded)
	if !ok {
		t.Fatal("decode must succeed")
	}
	if tc.Name != "noop" {
		t.Errorf("name: %s", tc.Name)
	}
	if string(tc.Arguments) != `{"k":1}` {
		t.Errorf("args: %s", string(tc.Arguments))
	}
}

func TestDecodeToolCallPayloadRejectsMalformed(t *testing.T) {
	if _, ok := DecodeToolCallPayload("no-separator"); ok {
		t.Fatal("missing separator must fail")
	}
	if _, ok := DecodeToolCallPayload("\x00"); ok {
		t.Fatal("empty name must fail")
	}
}
