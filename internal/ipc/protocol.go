package ipc

import (
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

type Request struct {
	Op        string `json:"op"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Query     string `json:"query,omitempty"`
}

type Response struct {
	OK          bool                  `json:"ok"`
	Op          string                `json:"op"`
	Message     string                `json:"message,omitempty"`
	RingState   string                `json:"ring_state,omitempty"`
	ServerMs    int64                 `json:"server_ms"`
	SessionID   string                `json:"session_id,omitempty"`
	Event       *bridge.StreamEvent   `json:"event,omitempty"`
	Suggestions []config.CommandEntry `json:"suggestions,omitempty"`
}
