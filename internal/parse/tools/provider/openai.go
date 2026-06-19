// Package provider contains parsers for OpenAI-compatible and Anthropic
// streaming APIs. They normalise wire events into the canonical
// parse.ToolCall / parse.ParsedThinking used by the rest of SapaLOQ.
package provider

import (
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// OpenAIToolDelta mirrors the streaming `delta.tool_calls` entry used by
// OpenAI Chat Completions, OpenRouter, TokenRouter, and Kimi (OpenAI shape).
type OpenAIToolDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

// AccumulatorOpenAI collects streaming tool-call deltas. Deltas with the same
// `index` are merged into a single call: the `id`, `type`, and `function.name`
// fields are taken from the first delta that supplies them; `function.arguments`
// fragments are concatenated and validated as JSON when complete.
type AccumulatorOpenAI struct {
	byIndex map[int]*parse.ToolCall
	order   []int
	source  string
}

// NewAccumulatorOpenAI returns an empty accumulator. The source label is
// stamped onto every produced parse.ToolCall (e.g. "openai_inline").
func NewAccumulatorOpenAI(source string) *AccumulatorOpenAI {
	if source == "" {
		source = "openai_inline"
	}
	return &AccumulatorOpenAI{byIndex: map[int]*parse.ToolCall{}, source: source}
}

// Apply merges a single streaming delta into the accumulator. Returns true if
// the delta carries new data (i.e. an id, name, or arguments fragment).
func (a *AccumulatorOpenAI) Apply(d OpenAIToolDelta) bool {
	tc, ok := a.byIndex[d.Index]
	if !ok {
		tc = &parse.ToolCall{Source: a.source}
		a.byIndex[d.Index] = tc
		a.order = append(a.order, d.Index)
	}
	if d.ID != "" {
		tc.ID = d.ID
	}
	if d.Type != "" {
		// type carries no semantic value for downstream consumers; keep name.
		_ = d.Type
	}
	if d.Function.Name != "" {
		tc.Name = d.Function.Name
	}
	if d.Function.Arguments != "" {
		// Late coalescing: if previous raw string is empty, take the new one
		// as-is. Otherwise concatenate.
		args := append(tc.Arguments, []byte(d.Function.Arguments)...)
		tc.Arguments = args
		return true
	}
	return d.ID != "" || d.Function.Name != ""
}

// Finish returns every accumulated tool call in stream order. Each call's
// Arguments is trimmed and validated: invalid JSON is left as the raw bytes
// so callers can decide whether to surface a leak.
func (a *AccumulatorOpenAI) Finish() []parse.ToolCall {
	out := make([]parse.ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		tc, ok := a.byIndex[idx]
		if !ok {
			continue
		}
		finished := *tc
		if len(finished.Arguments) > 0 {
			trimmed := strings.TrimSpace(string(finished.Arguments))
			if json.Valid([]byte(trimmed)) {
				finished.Arguments = json.RawMessage(trimmed)
			}
		}
		if strings.TrimSpace(finished.Name) == "" {
			continue
		}
		out = append(out, finished)
	}
	return out
}

// WithSource overrides the source label stamped onto produced tool calls.
func (a *AccumulatorOpenAI) WithSource(src string) *AccumulatorOpenAI {
	if src != "" {
		a.source = src
	}
	return a
}
