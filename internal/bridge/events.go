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
	// EventToolUpdate reports the durable lifecycle of an asynchronously
	// scheduled tool job. Tool calls are accepted by the run actor, executed by
	// scheduler workers, and correlated back through JobID/RunID.
	EventToolUpdate EventKind = "tool_update"
	// EventDecisionUpdate and EventSteeringUpdate are actor-to-actor control
	// events. They never write directly to the user chat; the session/UI
	// orchestrator is the single writer when a decision must be escalated.
	EventDecisionUpdate EventKind = "decision_update"
	EventSteeringUpdate EventKind = "steering_update"
	// EventTaskUpdate is a push from the orchestrator to the widget when a
	// background sub-agent reaches a terminal (or notable) state - the
	// completion trigger that lets the chat surface "task done/failed" without
	// the user polling. Carries TaskID/Role/Status plus a human Summary.
	EventTaskUpdate EventKind = "task_update"
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
	WaitSeconds int `json:"wait_seconds,omitempty"`
	// Task* fields are populated on EventTaskUpdate (background sub-agent
	// lifecycle pushes). TaskStatus mirrors taskRecord.Status (done/failed/
	// awaiting_clarification/stopped); Summary is a short human line.
	TaskID        string    `json:"task_id,omitempty"`
	TaskRole      string    `json:"task_role,omitempty"`
	TaskStatus    string    `json:"task_status,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	RunID         string    `json:"run_id,omitempty"`
	JobID         string    `json:"job_id,omitempty"`
	ParentID      string    `json:"parent_id,omitempty"`
	TargetID      string    `json:"target_id,omitempty"`
	EventID       string    `json:"event_id,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	Version       int64     `json:"version,omitempty"`
	At            time.Time `json:"at"`
}

func NewEvent(kind EventKind) StreamEvent {
	return StreamEvent{Kind: kind, At: time.Now().UTC()}
}
