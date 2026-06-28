package appserver

import (
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

const (
	StatusSession  = "session"
	StatusWorking  = "working"
	StatusToolDone = "tool_done"
)

type Mapper struct {
	sessionID string
	streamed  map[string]bool
}

func NewMapper(sessionID string) *Mapper {
	return &Mapper{sessionID: sessionID, streamed: make(map[string]bool)}
}

func (m *Mapper) Map(n Notification) []bridge.StreamEvent {
	switch n.Method {
	case "thread/started":
		return []bridge.StreamEvent{m.status(StatusSession, "")}
	case "turn/started":
		return []bridge.StreamEvent{m.status("Codex sedang bekerja…", "")}
	case "thread/tokenUsage/updated":
		return []bridge.StreamEvent{m.status("token_usage", string(n.Params))}
	case "item/agentMessage/delta":
		id, delta := deltaFields(n.Params)
		if delta == "" {
			return nil
		}
		m.streamed[id] = true
		return []bridge.StreamEvent{m.delta(bridge.EventResponseDelta, delta)}
	case "item/reasoning/textDelta", "item/reasoning/summaryTextDelta":
		id, delta := deltaFields(n.Params)
		if delta == "" {
			return nil
		}
		m.streamed[id] = true
		return []bridge.StreamEvent{m.delta(bridge.EventThinkingDelta, delta)}
	case "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
		id, delta := deltaFields(n.Params)
		if delta == "" {
			return nil
		}
		return []bridge.StreamEvent{m.toolOutput(id, delta)}
	case "item/fileChange/patchUpdated":
		return []bridge.StreamEvent{m.status("file_patch", string(n.Params))}
	case "item/started":
		return m.mapItem(n.Params, false)
	case "item/completed":
		return m.mapItem(n.Params, true)
	case "thread/compacted":
		return []bridge.StreamEvent{m.status("compacted", "")}
	default:
		debug.Verbosef("codex-bridge: skipping app-server notification %q", n.Method)
		return nil
	}
}

func (m *Mapper) mapItem(raw json.RawMessage, completed bool) []bridge.StreamEvent {
	var payload struct {
		Item json.RawMessage `json:"item"`
	}
	if json.Unmarshal(raw, &payload) != nil || len(payload.Item) == 0 {
		return nil
	}
	var item struct {
		ID               string          `json:"id"`
		Type             string          `json:"type"`
		Text             string          `json:"text"`
		Command          string          `json:"command"`
		Status           string          `json:"status"`
		ExitCode         *int            `json:"exitCode"`
		AggregatedOutput string          `json:"aggregatedOutput"`
		Server           string          `json:"server"`
		Tool             string          `json:"tool"`
		Query            string          `json:"query"`
		Path             string          `json:"path"`
		Arguments        json.RawMessage `json:"arguments"`
		Summary          []string        `json:"summary"`
		Content          []string        `json:"content"`
	}
	if json.Unmarshal(payload.Item, &item) != nil {
		return nil
	}
	switch item.Type {
	case "agentMessage":
		if completed && item.Text != "" && !m.streamed[item.ID] {
			return []bridge.StreamEvent{m.delta(bridge.EventResponseDelta, item.Text)}
		}
	case "reasoning":
		if completed && !m.streamed[item.ID] {
			text := strings.Join(append(item.Summary, item.Content...), "\n")
			if text != "" {
				return []bridge.StreamEvent{m.delta(bridge.EventThinkingDelta, text)}
			}
		}
	case "dynamicToolCall":
		// item/tool/call emits telemetry from the server-request handler so the
		// call is visible before its potentially long execution and never twice.
		return nil
	case "commandExecution", "fileChange", "mcpToolCall", "webSearch", "imageGeneration", "imageView", "collabAgentToolCall":
		if !completed {
			args := codexNativeArgs(item.Type, item.Command, item.Path, item.Query, item.Arguments, payload.Item)
			return []bridge.StreamEvent{m.nativeToolCall(item.ID, item.Type, args)}
		}
		status := "completed"
		if item.ExitCode != nil && *item.ExitCode != 0 {
			status = "failed"
		}
		return []bridge.StreamEvent{m.toolComplete(item.ID, item.AggregatedOutput, status)}
	}
	return nil
}

func (m *Mapper) toolOutput(itemID, chunk string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventToolUpdate)
	ev.SessionID = m.sessionID
	ev.ToolCall = &parse.ToolCall{ID: itemID, Source: "codex"}
	ev.ToolResult = chunk
	ev.Status = "running"
	return ev
}

func (m *Mapper) toolComplete(itemID, result, status string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventToolUpdate)
	ev.SessionID = m.sessionID
	ev.ToolCall = &parse.ToolCall{ID: itemID, Source: "codex"}
	ev.ToolResult = result
	ev.Status = status
	return ev
}

func codexNativeArgs(itemType, command, path, query string, args json.RawMessage, rawItem json.RawMessage) json.RawMessage {
	switch itemType {
	case "commandExecution":
		b, _ := json.Marshal(map[string]string{"command": strings.TrimSpace(command)})
		return b
	case "fileChange":
		b, _ := json.Marshal(map[string]string{"path": strings.TrimSpace(path)})
		return b
	case "webSearch":
		b, _ := json.Marshal(map[string]string{"query": strings.TrimSpace(query)})
		return b
	default:
		if len(args) > 0 {
			return args
		}
		if len(rawItem) > 0 && len(rawItem) <= 4096 {
			return rawItem
		}
		b, _ := json.Marshal(map[string]string{"type": itemType})
		return b
	}
}

func (m *Mapper) nativeToolCall(id, name string, args json.RawMessage) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventToolCall)
	ev.SessionID = m.sessionID
	ev.ToolCall = &parse.ToolCall{ID: id, Name: name, Arguments: args, Source: "codex"}
	return ev
}

func (m *Mapper) DynamicToolCall(call DynamicToolCallParams) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventToolCall)
	ev.SessionID = m.sessionID
	name := call.Tool
	if call.Namespace != "" && call.Namespace != "sapaloq" {
		name = call.Namespace + "." + call.Tool
	}
	ev.ToolCall = &parse.ToolCall{ID: call.CallID, Name: name, Arguments: call.Arguments, Source: "codex"}
	return ev
}

func (m *Mapper) delta(kind bridge.EventKind, text string) bridge.StreamEvent {
	ev := bridge.NewEvent(kind)
	ev.SessionID = m.sessionID
	ev.Delta = text
	return ev
}

func (m *Mapper) status(status, delta string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventStatus)
	ev.SessionID = m.sessionID
	ev.Status = status
	ev.Delta = delta
	return ev
}

func deltaFields(raw json.RawMessage) (string, string) {
	var p struct {
		ItemID string `json:"itemId"`
		Delta  string `json:"delta"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.ItemID, p.Delta
}
