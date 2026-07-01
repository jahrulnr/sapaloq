package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// toolCallTrace records one tool call announced this inference turn and whether
// a matching EventToolUpdate arrived (in-bridge MCP or orchestrator batch).
type toolCallTrace struct {
	call     parse.ToolCall
	resolved bool
}

func trackToolCall(traces *[]toolCallTrace, call parse.ToolCall) {
	*traces = append(*traces, toolCallTrace{call: call})
}

func resolveToolCall(traces *[]toolCallTrace, call *parse.ToolCall) {
	if call == nil {
		return
	}
	id := call.ID
	for i := len(*traces) - 1; i >= 0; i-- {
		t := &(*traces)[i]
		if t.resolved {
			continue
		}
		if id != "" && t.call.ID == id {
			t.resolved = true
			return
		}
		if id == "" && t.call.Name == call.Name {
			t.resolved = true
			return
		}
	}
}

func unresolvedToolCalls(traces []toolCallTrace) []parse.ToolCall {
	out := make([]parse.ToolCall, 0, len(traces))
	for _, t := range traces {
		if !t.resolved {
			out = append(out, t.call)
		}
	}
	return out
}

func malformedToolFailureResult(call parse.ToolCall) string {
	args := strings.TrimSpace(string(call.Arguments))
	if args == "" {
		args = "(empty)"
	}
	src := strings.TrimSpace(call.Source)
	if src == "" {
		src = "unknown"
	}
	return fmt.Sprintf(
		"malformed tool call — not executed\nname: %s\nsource: %s\nargs: %s",
		call.Name, src, args,
	)
}

// surfaceUnresolvedToolFailures emits failed tool rows for calls that were
// announced (EventToolCall) but never completed (no matching ToolUpdate).
func (o *Orchestrator) surfaceUnresolvedToolFailures(
	ctx context.Context,
	out turnSink,
	sessionID, persistID, generationID string,
	cfg turnConfig,
	response, turnThinking *strings.Builder,
	cleanMessages *[]bridge.Message,
	calls []parse.ToolCall,
) {
	for _, call := range calls {
		c := call
		update := bridge.NewEvent(bridge.EventToolUpdate)
		update.SessionID = sessionID
		update.ToolCall = &c
		update.ToolResult = malformedToolFailureResult(c)
		update.Status = "failed"
		out.emit(ctx, update)
		if isInBridgeToolSource(c.Source) {
			o.appendInBridgeToolUpdate(ctx, persistID, generationID, cfg, response, turnThinking, cleanMessages, update)
		} else if cfg.recordToolTurns && o.chat != nil && persistID != "" {
			body := toolObservationBody([]string{update.ToolResult})
			o.persistToolTurn(ctx, persistID, body, generationID)
		}
	}
}
