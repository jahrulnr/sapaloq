package ipc

import (
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/core/orchestrator"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

type Request struct {
	Op         string `json:"op"`
	Message    string `json:"message,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	TurnID     int64  `json:"turn_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	AfterLine  int    `json:"after_line,omitempty"`
	Scope      string `json:"scope,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
	Priority   string `json:"priority,omitempty"`
	Query      string `json:"query,omitempty"`
	Signal     string `json:"signal,omitempty"`
	Correction string `json:"correction,omitempty"`
	Path       string `json:"path,omitempty"`
	// SegmentCheckpoint selects a compaction segment for chat_history_segment:
	// -1 latest tail, 0 pre-first checkpoint, k>0 historical anchor. Nil → latest.
	SegmentCheckpoint *int `json:"segment_checkpoint,omitempty"`
}

type Response struct {
	OK           bool                             `json:"ok"`
	Op           string                           `json:"op"`
	Message      string                           `json:"message,omitempty"`
	RingState    string                           `json:"ring_state,omitempty"`
	ServerMs     int64                            `json:"server_ms"`
	SessionID    string                           `json:"session_id,omitempty"`
	Event        *bridge.StreamEvent              `json:"event,omitempty"`
	Suggestions  []config.CommandEntry            `json:"suggestions,omitempty"`
	Turns        []chatstore.Turn                 `json:"turns,omitempty"`
	Timeline     []bridge.StreamEvent             `json:"timeline,omitempty"`
	Transcript   []bridge.TranscriptEntry         `json:"transcript,omitempty"`
	Reset        bool                             `json:"reset,omitempty"`
	Usage        *chatstore.Usage                 `json:"usage,omitempty"`
	Runtime      *orchestrator.RuntimeStatus      `json:"runtime,omitempty"`
	Sessions     []chatstore.SessionSummary       `json:"sessions,omitempty"`
	TaskInspect  *orchestrator.TaskInspectResult  `json:"task_inspect,omitempty"`
	ActorInspect *orchestrator.ActorInspectResult `json:"actor_inspect,omitempty"`
	Path         string                           `json:"path,omitempty"`
	TaskID       string                           `json:"task_id,omitempty"`
	SegmentCheckpoint int                         `json:"segment_checkpoint,omitempty"`
	HasOlder          bool                        `json:"has_older,omitempty"`
	OlderCheckpoint   int                         `json:"older_checkpoint,omitempty"`
	SessionWorkspace string               `json:"session_workspace,omitempty"`
	IsLatestSegment   bool                        `json:"is_latest_segment,omitempty"`
}
