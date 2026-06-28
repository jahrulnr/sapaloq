package agent

import (
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
)

// Mapper converts Agent API decoded events into bridge.StreamEvent slices.
type Mapper struct {
	sessionID string
	streamed  map[string]bool
}

func NewMapper(sessionID string) *Mapper {
	return &Mapper{sessionID: sessionID, streamed: make(map[string]bool)}
}

func (m *Mapper) Map(decoded []wire.AgentDecoded) []bridge.StreamEvent {
	var out []bridge.StreamEvent
	for _, d := range decoded {
		switch d.Kind {
		case "text":
			if d.Text == "" {
				continue
			}
			out = append(out, m.delta(bridge.EventResponseDelta, d.Text))
		case "thinking":
			if d.Thinking == "" {
				continue
			}
			out = append(out, m.delta(bridge.EventThinkingDelta, d.Thinking))
		case "tool_call_started", "tool_call_completed":
			// Generic markers without tool name/id — MCP exec emits real
			// EventToolCall rows via bridge_agent OnMCPTool; skip cursor_tool spam.
		case "token_delta":
			if d.Tokens > 0 {
				out = append(out, m.status("token_usage", ""))
			}
		case "thinking_complete", "heartbeat", "kv_server_message":
			// Telemetry-only markers; no user-visible delta.
		case "turn_ended":
			// Terminal marker handled by stream driver.
		}
	}
	return out
}

func (m *Mapper) delta(kind bridge.EventKind, text string) bridge.StreamEvent {
	ev := bridge.NewEvent(kind)
	ev.SessionID = m.sessionID
	ev.Delta = text
	return ev
}

func (m *Mapper) status(label, detail string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventStatus)
	ev.SessionID = m.sessionID
	ev.Status = label
	ev.Delta = detail
	return ev
}
