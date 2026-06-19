package provider

import (
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// AccumulatorKimi reuses the OpenAI streaming layout (Kimi is OpenAI-compatible
// at the HTTP layer) but tracks a separate reasoning buffer so the bridge can
// forward thinking text as EventThinkingDelta.
type AccumulatorKimi struct {
	openai *AccumulatorOpenAI
	source string
}

// NewAccumulatorKimi returns an empty accumulator. The source label is
// stamped onto every produced parse.ToolCall (e.g. "kimi_inline").
func NewAccumulatorKimi(source string) *AccumulatorKimi {
	if source == "" {
		source = "kimi_inline"
	}
	return &AccumulatorKimi{openai: NewAccumulatorOpenAI(source), source: source}
}

// Apply forwards to the underlying OpenAI accumulator. Kimi emits tool calls
// in the standard `delta.tool_calls` shape.
func (k *AccumulatorKimi) Apply(d OpenAIToolDelta) bool {
	return k.openai.Apply(d)
}

// Finish returns the accumulated tool calls.
func (k *AccumulatorKimi) Finish() []parse.ToolCall {
	return k.openai.Finish()
}

// WithSource overrides the source label stamped onto produced tool calls.
func (k *AccumulatorKimi) WithSource(src string) *AccumulatorKimi {
	if src != "" {
		k.source = src
		k.openai.WithSource(src)
	}
	return k
}
