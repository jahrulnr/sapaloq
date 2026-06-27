package orchestrator

import (
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestCoalesceEventsTextAndTool(t *testing.T) {
	now := time.Now().UTC()
	events := []bridge.StreamEvent{
		{Kind: bridge.EventResponseDelta, Delta: "Hello", At: now},
		{Kind: bridge.EventResponseDelta, Delta: " world", At: now},
		{Kind: bridge.EventToolCall, ToolCall: &parse.ToolCall{ID: "t1", Name: "read", Arguments: []byte(`{}`)}, At: now},
		{Kind: bridge.EventToolUpdate, ToolCall: &parse.ToolCall{ID: "t1", Name: "read"}, ToolResult: "ok", At: now},
	}
	out := CoalesceEvents("1", events)
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	if out[0].Kind != bridge.TranscriptText || out[0].Text != "Hello world" {
		t.Fatalf("unexpected text entry: %+v", out[0])
	}
	if out[1].Kind != bridge.TranscriptTool || out[1].ToolResult != "ok" {
		t.Fatalf("unexpected tool entry: %+v", out[1])
	}
}

func TestCoalesceAutopilotNudge(t *testing.T) {
	now := time.Now().UTC()
	events := []bridge.StreamEvent{
		{Kind: bridge.EventStatus, Status: "continuing - call sapaloq_stop", At: now},
	}
	out := CoalesceEvents("1", events)
	if len(out) != 0 {
		t.Fatalf("autopilot nudge must not surface in transcript, got %+v", out)
	}
}
