package agent

import (
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
)

func TestMapperTextAndThinking(t *testing.T) {
	m := NewMapper("sess-1")
	events := m.Map([]wire.AgentDecoded{
		{Kind: "thinking", Thinking: "hmm"},
		{Kind: "text", Text: "hello"},
		{Kind: "turn_ended"},
	})
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Kind != bridge.EventThinkingDelta || events[0].Delta != "hmm" {
		t.Fatalf("thinking = %+v", events[0])
	}
	if events[1].Kind != bridge.EventResponseDelta || events[1].Delta != "hello" {
		t.Fatalf("text = %+v", events[1])
	}
}

func TestMapperSkipsGenericToolCallMarkers(t *testing.T) {
	events := NewMapper("s").Map([]wire.AgentDecoded{
		{Kind: "tool_call_started"},
		{Kind: "tool_call_completed"},
		{Kind: "heartbeat"},
	})
	if len(events) != 0 {
		t.Fatalf("generic tool markers must not emit status rows: %+v", events)
	}
}
