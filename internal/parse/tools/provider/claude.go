package provider

import (
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// ClaudeBlockEvent models a single Anthropic Messages streaming event. We
// keep it as a tagged union so the bridge can dispatch on `type` without
// importing the official SDK.
type ClaudeBlockEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index,omitempty"`
	// content_block_start
	ContentBlock *ClaudeBlockStart `json:"content_block,omitempty"`
	// content_block_delta / message_delta
	Delta *ClaudeBlockDelta `json:"delta,omitempty"`
}

type ClaudeBlockStart struct {
	Type  string          `json:"type"` // text | thinking | tool_use | redacted_thinking
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// thinking blocks also carry text/signature on start
	Text      string `json:"text,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type ClaudeBlockDelta struct {
	Type        string `json:"type"` // text_delta | thinking_delta | input_json_delta | signature_delta
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Signature   string `json:"signature,omitempty"`
	// message_delta-only
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
}

// AccumulatorClaude walks a sequence of Anthropic streaming events and emits
// tool calls and thinking text. It mirrors AccumulatorOpenAI but matches the
// content_block_* event family.
type AccumulatorClaude struct {
	tools    map[int]*parse.ToolCall
	order    []int
	thinking strings.Builder
	text     strings.Builder
	source   string
}

// NewAccumulatorClaude returns an empty accumulator.
func NewAccumulatorClaude(source string) *AccumulatorClaude {
	if source == "" {
		source = "claude_inline"
	}
	return &AccumulatorClaude{tools: map[int]*parse.ToolCall{}, source: source}
}

// Apply dispatches one Claude streaming event. Returns the deltas that should
// be surfaced: a non-empty toolCall payload means a tool_use block is complete;
// a non-empty thinking/text means that stream should emit EventThinkingDelta
// or EventResponseDelta respectively.
func (a *AccumulatorClaude) Apply(ev ClaudeBlockEvent) (thinking, text, toolCall string) {
	switch ev.Type {
	case "content_block_start":
		return a.handleBlockStart(ev)
	case "content_block_delta":
		return a.handleBlockDelta(ev)
	case "content_block_stop":
		return a.handleBlockStop(ev)
	}
	return
}

func (a *AccumulatorClaude) handleBlockStart(ev ClaudeBlockEvent) (thinking, text, toolCall string) {
	cb := ev.ContentBlock
	if cb == nil {
		return
	}
	switch cb.Type {
	case "tool_use":
		tc := &parse.ToolCall{
			ID:     cb.ID,
			Name:   cb.Name,
			Source: a.source,
		}
		if len(cb.Input) > 0 {
			tc.Arguments = cb.Input
		}
		a.tools[ev.Index] = tc
		a.order = append(a.order, ev.Index)
	case "thinking":
		a.thinking.WriteString(cb.Thinking)
	}
	return
}

func (a *AccumulatorClaude) handleBlockDelta(ev ClaudeBlockEvent) (thinking, text, toolCall string) {
	d := ev.Delta
	if d == nil {
		return
	}
	switch d.Type {
	case "thinking_delta":
		a.thinking.WriteString(d.Thinking)
		return d.Thinking, "", ""
	case "text_delta":
		a.text.WriteString(d.Text)
		return "", d.Text, ""
	case "input_json_delta":
		if tc, ok := a.tools[ev.Index]; ok {
			tc.Arguments = append(tc.Arguments, []byte(d.PartialJSON)...)
		}
	case "signature_delta":
		// Signature deltas accompany the final thinking block; discard.
	}
	return
}

func (a *AccumulatorClaude) handleBlockStop(ev ClaudeBlockEvent) (thinking, text, toolCall string) {
	tc, ok := a.tools[ev.Index]
	if !ok {
		return
	}
	finished := *tc
	raw := strings.TrimSpace(string(finished.Arguments))
	if json.Valid([]byte(raw)) {
		finished.Arguments = json.RawMessage(raw)
	}
	if strings.TrimSpace(finished.Name) != "" {
		toolCall = finished.Name + "\x00" + string(finished.Arguments)
	}
	delete(a.tools, ev.Index)
	return
}

// Finish returns any tool calls that completed without an explicit stop event
// (shouldn't normally happen, but protects against truncated streams).
func (a *AccumulatorClaude) Finish() []parse.ToolCall {
	out := make([]parse.ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		if tc, ok := a.tools[idx]; ok {
			if strings.TrimSpace(tc.Name) == "" {
				continue
			}
			out = append(out, *tc)
		}
	}
	return out
}

// WithSource overrides the source label stamped onto produced tool calls.
func (a *AccumulatorClaude) WithSource(src string) *AccumulatorClaude {
	if src != "" {
		a.source = src
	}
	return a
}
