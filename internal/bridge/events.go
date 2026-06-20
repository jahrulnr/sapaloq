package bridge

import (
	"time"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

type EventKind string

const (
	EventThinkingDelta EventKind = "thinking_delta"
	EventResponseDelta EventKind = "response_delta"
	EventToolCall      EventKind = "tool_call"
	EventToolLeak      EventKind = "tool_leak"
	EventStatus        EventKind = "status"
	EventDone          EventKind = "done"
	EventError         EventKind = "error"
)

type StreamEvent struct {
	Kind      EventKind       `json:"kind"`
	SessionID string          `json:"session_id,omitempty"`
	Delta     string          `json:"delta,omitempty"`
	ToolCall  *parse.ToolCall `json:"tool_call,omitempty"`
	Leak      string          `json:"leak,omitempty"`
	Error     string          `json:"error,omitempty"`
	Status    string          `json:"status,omitempty"`
	// WaitSeconds carries the effective wait window for a "waiting" status so
	// the UI can render a live countdown (e.g. 10s, 9s, ...). Zero when N/A.
	WaitSeconds int       `json:"wait_seconds,omitempty"`
	At          time.Time `json:"at"`
}

func NewEvent(kind EventKind) StreamEvent {
	return StreamEvent{Kind: kind, At: time.Now().UTC()}
}
