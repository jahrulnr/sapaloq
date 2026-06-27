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

func TestCoalesceEventsSkipsTaskUpdateCards(t *testing.T) {
	now := time.Now().UTC()
	events := []bridge.StreamEvent{
		{Kind: bridge.EventResponseDelta, Delta: "working", At: now},
		{
			Kind: bridge.EventTaskUpdate, SessionID: "s1", TaskID: "task-1",
			TaskRole: "task-runner", TaskStatus: "in_progress",
			Summary: "Menjalankan `exec`.", At: now,
		},
	}
	out := CoalesceEvents("task-1", events)
	if len(out) != 1 || out[0].Kind != bridge.TranscriptText {
		t.Fatalf("task cards must not appear in sub-agent replay, got %+v", out)
	}
}

func TestEntriesWithPendingStreamsPartialText(t *testing.T) {
	c := NewTranscriptCoalescer("42")
	c.Apply(bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "Hel"})
	c.Apply(bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "lo"})
	if len(c.Entries()) != 0 {
		t.Fatalf("buffered text must not flush to Entries() yet, got %d", len(c.Entries()))
	}
	pending := c.EntriesWithPending()
	if len(pending) != 1 || pending[0].Text != "Hello" {
		t.Fatalf("pending snapshot = %+v, want single Hello row", pending)
	}
	if pending[0].ID != "42-pending-text" {
		t.Fatalf("pending id = %q, want stable id for DOM patch", pending[0].ID)
	}
}
