package bridge

import "time"

// TranscriptEntryKind is a coalesced UI row for the widget transcript pane.
type TranscriptEntryKind string

const (
	TranscriptUser       TranscriptEntryKind = "user"
	TranscriptThinking   TranscriptEntryKind = "thinking"
	TranscriptText       TranscriptEntryKind = "text"
	TranscriptTool       TranscriptEntryKind = "tool"
	TranscriptStatus     TranscriptEntryKind = "status"
	TranscriptTask       TranscriptEntryKind = "task"
	TranscriptCheckpoint TranscriptEntryKind = "checkpoint"
	TranscriptError      TranscriptEntryKind = "error"
	TranscriptProgress   TranscriptEntryKind = "progress"
)

// TranscriptEntry is the BE-driven view model for one transcript row.
type TranscriptEntry struct {
	ID           string              `json:"id"`
	Kind         TranscriptEntryKind `json:"kind"`
	GenerationID string              `json:"generation_id,omitempty"`
	TurnID       int64               `json:"turn_id,omitempty"`
	Seq          int                 `json:"seq,omitempty"`
	At           time.Time           `json:"at"`
	Archived     bool                `json:"archived,omitempty"`

	Text string `json:"text,omitempty"`
	// Tool fields
	ToolID     string `json:"tool_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolArgs   string `json:"tool_args,omitempty"`
	ToolResult string `json:"tool_result,omitempty"`
	ToolStatus string `json:"tool_status,omitempty"`
	// Task card fields
	TaskID     string `json:"task_id,omitempty"`
	TaskRole   string `json:"task_role,omitempty"`
	TaskStatus string `json:"task_status,omitempty"`
	Summary    string `json:"summary,omitempty"`
	// Checkpoint fields
	CheckpointIndex  int    `json:"checkpoint_index,omitempty"`
	CheckpointReason string `json:"checkpoint_reason,omitempty"`
	// Progress / status label
	Label       string `json:"label,omitempty"`
	WaitSeconds int    `json:"wait_seconds,omitempty"`
}

// TranscriptPatch is a live snapshot for one chat generation.
type TranscriptPatch struct {
	SessionID       string            `json:"session_id"`
	ActorID         string            `json:"actor_id,omitempty"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	GenerationID    string            `json:"generation_id"`
	Entries         []TranscriptEntry `json:"entries"`
	Finished        bool              `json:"finished,omitempty"`
	TurnID          int64             `json:"turn_id,omitempty"`
	// Reset tells the widget to discard the current transcript DOM and replace
	// it with Entries (usually empty for a fresh session). Only set after the
	// backend has persisted the new/empty session so the UI never clears on a
	// failed reset.
	Reset bool `json:"reset,omitempty"`
	// Usage is attached on turn boundaries, checkpoints, and terminal events so
	// the context pill updates without querying on every streaming delta.
	Usage *TranscriptUsage `json:"usage,omitempty"`
}

// TranscriptUsage is a lightweight context-window snapshot for the widget pill.
type TranscriptUsage struct {
	UsedTokens    int    `json:"used_tokens"`
	ContextWindow int    `json:"context_window"`
	Percent       int    `json:"percent"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
}
