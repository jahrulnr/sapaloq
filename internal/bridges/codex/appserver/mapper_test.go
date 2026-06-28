package appserver

import (
	"encoding/json"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func TestMapperStreamsDeltasWithoutCompletedDuplicates(t *testing.T) {
	m := NewMapper("session-1")
	events := m.Map(Notification{Method: "item/agentMessage/delta", Params: json.RawMessage(`{"itemId":"a1","delta":"hello"}`)})
	if len(events) != 1 || events[0].Kind != bridge.EventResponseDelta || events[0].Delta != "hello" {
		t.Fatalf("delta events = %+v", events)
	}
	events = m.Map(Notification{Method: "item/completed", Params: json.RawMessage(`{"item":{"id":"a1","type":"agentMessage","text":"hello"}}`)})
	if len(events) != 0 {
		t.Fatalf("completed duplicated streamed message: %+v", events)
	}
}

func TestMapperCompletedFallbackAndNativeToolTelemetry(t *testing.T) {
	m := NewMapper("session-1")
	events := m.Map(Notification{Method: "item/completed", Params: json.RawMessage(`{"item":{"id":"a1","type":"agentMessage","text":"batch"}}`)})
	if len(events) != 1 || events[0].Delta != "batch" {
		t.Fatalf("batch event = %+v", events)
	}
	events = m.Map(Notification{Method: "item/started", Params: json.RawMessage(`{"item":{"id":"c1","type":"commandExecution","command":"pwd","status":"inProgress"}}`)})
	if len(events) != 1 || events[0].ToolCall == nil || events[0].ToolCall.Source != "codex" || events[0].ToolCall.Name != "commandExecution" {
		t.Fatalf("tool event = %+v", events)
	}
}

func TestMapperUnknownNotificationIsTolerated(t *testing.T) {
	if got := NewMapper("s").Map(Notification{Method: "future/event", Params: json.RawMessage(`{}`)}); got != nil {
		t.Fatalf("unknown notification = %+v", got)
	}
}
