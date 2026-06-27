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
	Query      string `json:"query,omitempty"`
	Signal     string `json:"signal,omitempty"`
	Correction string `json:"correction,omitempty"`
}

type Response struct {
	OK          bool                            `json:"ok"`
	Op          string                          `json:"op"`
	Message     string                          `json:"message,omitempty"`
	RingState   string                          `json:"ring_state,omitempty"`
	ServerMs    int64                           `json:"server_ms"`
	SessionID   string                          `json:"session_id,omitempty"`
	Event       *bridge.StreamEvent             `json:"event,omitempty"`
	Suggestions []config.CommandEntry           `json:"suggestions,omitempty"`
	Turns       []chatstore.Turn                `json:"turns,omitempty"`
	Timeline    []bridge.StreamEvent            `json:"timeline,omitempty"`
	Transcript  []bridge.TranscriptEntry        `json:"transcript,omitempty"`
	Reset       bool                            `json:"reset,omitempty"`
	Usage       *chatstore.Usage                `json:"usage,omitempty"`
	Runtime     *orchestrator.RuntimeStatus      `json:"runtime,omitempty"`
	Sessions    []chatstore.SessionSummary      `json:"sessions,omitempty"`
	TaskInspect *orchestrator.TaskInspectResult `json:"task_inspect,omitempty"`
}
