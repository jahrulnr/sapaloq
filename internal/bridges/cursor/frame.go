package cursor

import (
	"encoding/json"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	toolcursor "github.com/jahrulnr/sapaloq/internal/parse/tools/cursor"
	"github.com/jahrulnr/sapaloq/internal/parse/tools/kimi"
)

type Frame struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	Tool json.RawMessage `json:"tool,omitempty"`
}

func DecodeFrame(schema Schema, b []byte) []bridge.StreamEvent {
	var frame Frame
	if err := json.Unmarshal(b, &frame); err != nil {
		return []bridge.StreamEvent{{Kind: bridge.EventError, Error: err.Error()}}
	}
	switch frame.Type {
	case "thinking":
		ev := bridge.NewEvent(bridge.EventThinkingDelta)
		ev.Delta = frame.Text
		return []bridge.StreamEvent{ev}
	case "response":
		events := []bridge.StreamEvent{}
		if frame.Text != "" {
			extracted := kimi.ExtractWithTokens(frame.Text, schema.KimiTokens())
			if extracted.CleanedText != "" {
				ev := bridge.NewEvent(bridge.EventResponseDelta)
				ev.Delta = extracted.CleanedText
				events = append(events, ev)
			}
			for _, call := range extracted.Calls {
				coerced := CoerceToolCall(schema, call)
				ev := bridge.NewEvent(bridge.EventToolCall)
				ev.ToolCall = &coerced
				events = append(events, ev)
			}
		}
		return events
	case "tool_call":
		if call, ok := toolcursor.ParseClientSideToolV2Call(frame.Tool); ok {
			coerced := CoerceToolCall(schema, call)
			ev := bridge.NewEvent(bridge.EventToolCall)
			ev.ToolCall = &coerced
			return []bridge.StreamEvent{ev}
		}
	}
	return nil
}
