package provider

import (
	"bytes"
	"encoding/json"

	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/parse"
	toolprovider "github.com/jahrulnr/sapaloq/internal/parse/tools/provider"
)

// openAILineHandler is the SSE line dispatcher for OpenAI / OpenRouter /
// TokenRouter streams. The returned error is one of: nil (keep streaming),
// errStreamStopped (the bridge hung up), or any other error which is
// surfaced as EventError upstream.
type openAILineHandler struct {
	acc *toolprovider.AccumulatorOpenAI
	on  WireHandler
}

func newOpenAILineHandler(acc *toolprovider.AccumulatorOpenAI, on WireHandler) *openAILineHandler {
	return &openAILineHandler{acc: acc, on: on}
}

func (h *openAILineHandler) Handle(line []byte) error {
	payload, ok := extractDataPayload(line)
	if !ok {
		return nil
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		return h.flushAndStop()
	}
	var chunk openAIChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		debug.Debugf("provider-bridge(openai): chunk parse err: %v", err)
		return nil
	}
	for _, choice := range chunk.Choices {
		if !h.emitDeltas(choice.Delta) {
			return errStreamStopped
		}
	}
	return nil
}

// emitDeltas surfaces a single choice's delta. Returns false when the bridge
// has stopped accepting events.
func (h *openAILineHandler) emitDeltas(delta openAIDelta) bool {
	if delta.Reasoning != "" && !h.on(WireEvent{Thinking: delta.Reasoning}) {
		return false
	}
	if delta.Content != "" && !h.on(WireEvent{Text: delta.Content}) {
		return false
	}
	for _, tc := range delta.ToolCalls {
		h.acc.Apply(openAIToToolDelta(tc))
	}
	return true
}

// flushAndStop emits every accumulated tool call and signals stream stop.
func (h *openAILineHandler) flushAndStop() error {
	for _, tc := range h.acc.Finish() {
		if !h.on(WireEvent{Tool: tc}) {
			return errStreamStopped
		}
	}
	return errStreamStopped
}

// kimiLineHandler is the SSE line dispatcher for Kimi (Moonshot) streams.
// The wire format is identical to OpenAI; only the `thinking` body field and
// `reasoning_content` delta differ, both of which the generic openAI structs
// already cover.
type kimiLineHandler struct {
	acc *toolprovider.AccumulatorKimi
	on  WireHandler
}

func newKimiLineHandler(acc *toolprovider.AccumulatorKimi, on WireHandler) *kimiLineHandler {
	return &kimiLineHandler{acc: acc, on: on}
}

func (h *kimiLineHandler) Handle(line []byte) error {
	payload, ok := extractDataPayload(line)
	if !ok {
		return nil
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		return h.flushAndStop()
	}
	var chunk openAIChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		debug.Debugf("provider-bridge(kimi): chunk parse err: %v", err)
		return nil
	}
	for _, choice := range chunk.Choices {
		if !h.emitDeltas(choice.Delta) {
			return errStreamStopped
		}
	}
	return nil
}

// emitDeltas surfaces a single choice's delta to the bridge. Returns false
// when the bridge has stopped accepting events.
func (h *kimiLineHandler) emitDeltas(delta openAIDelta) bool {
	if delta.Reasoning != "" && !h.on(WireEvent{Thinking: delta.Reasoning}) {
		return false
	}
	if delta.Content != "" && !h.on(WireEvent{Text: delta.Content}) {
		return false
	}
	for _, tc := range delta.ToolCalls {
		h.acc.Apply(openAIToToolDelta(tc))
	}
	return true
}

// flushAndStop emits every accumulated tool call and signals stream stop.
func (h *kimiLineHandler) flushAndStop() error {
	for _, tc := range h.acc.Finish() {
		if !h.on(WireEvent{Tool: tc}) {
			return errStreamStopped
		}
	}
	return errStreamStopped
}

// claudeLineHandler is the SSE line dispatcher for Anthropic Messages streams.
// Anthropic uses `event: <type>` + `data: <json>` pairs separated by blank
// lines, so we ignore the `event:` lines and parse only the `data:` payloads.
type claudeLineHandler struct {
	acc *toolprovider.AccumulatorClaude
	on  WireHandler
}

func newClaudeLineHandler(acc *toolprovider.AccumulatorClaude, on WireHandler) *claudeLineHandler {
	return &claudeLineHandler{acc: acc, on: on}
}

func (h *claudeLineHandler) Handle(line []byte) error {
	// Skip event kind lines; we only care about the JSON payload.
	if bytes.HasPrefix(line, []byte("event:")) {
		return nil
	}
	payload, ok := extractDataPayload(line)
	if !ok {
		return nil
	}
	var ev toolprovider.ClaudeBlockEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		debug.Debugf("provider-bridge(claude): event parse err: %v", err)
		return nil
	}
	thinking, text, toolPayload := h.acc.Apply(ev)
	if thinking != "" && !h.on(WireEvent{Thinking: thinking}) {
		return errStreamStopped
	}
	if text != "" && !h.on(WireEvent{Text: text}) {
		return errStreamStopped
	}
	if toolPayload == "" {
		return nil
	}
	tc, ok := toolprovider.DecodeToolCallPayload(toolPayload)
	if !ok {
		return nil
	}
	if !h.on(WireEvent{Tool: tc}) {
		return errStreamStopped
	}
	return nil
}

// extractDataPayload strips the "data: " prefix (if present) and returns
// the trimmed payload bytes. Returns false for blank, comment (":"), or
// non-data lines.
func extractDataPayload(line []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] == ':' {
		return nil, false
	}
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return nil, false
	}
	return bytes.TrimSpace(trimmed[len("data:"):]), true
}

// compile-time guard: the parse package is referenced so future changes
// that drop parse.ToolCall break the build rather than silent drift.
var _ = parse.ToolCall{}
