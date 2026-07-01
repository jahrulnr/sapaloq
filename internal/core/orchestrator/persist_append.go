package orchestrator

import (
	"context"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse/artifacts"
)

func isInBridgeToolSource(source string) bool {
	return source == "codex" || source == "cursor"
}

// appendInBridgeToolUpdate persists one in-bridge MCP ToolUpdate in wire order:
// tool row first, then assistant narration/note when available. Replay order for
// the model API is fixed in actorTurnsToMessages.
func (o *Orchestrator) appendInBridgeToolUpdate(
	ctx context.Context,
	persistID, generationID string,
	cfg turnConfig,
	response, turnThinking *strings.Builder,
	cleanMessages *[]bridge.Message,
	ev bridge.StreamEvent,
) {
	if o == nil || !cfg.recordToolTurns || ev.ToolCall == nil || !isInBridgeToolSource(ev.ToolCall.Source) {
		return
	}
	if o.chat == nil || persistID == "" {
		return
	}

	call := *ev.ToolCall
	result := strings.TrimSpace(ev.ToolResult)
	if result == "" && ev.Status == "failed" {
		result = "tool failed"
	}
	redacted := o.redactToolResults([]string{result})
	if len(redacted) == 0 {
		redacted = []string{result}
	}
	toolBody := toolObservationBody(redacted)

	note := calledToolsNote([]scheduledTool{{call: call}})
	assistantContent := strings.TrimSpace(artifacts.StripModelResponseArtifact(StripCalledToolsMarkers(response.String())))
	if artifacts.IsAutopilotEcho(assistantContent) {
		assistantContent = ""
	}
	if note != "" {
		if assistantContent != "" {
			assistantContent += "\n\n"
		}
		assistantContent += note
	}

	o.flushTurnThinking(ctx, persistID, generationID, cfg, turnThinking)
	if toolBody != "" {
		o.persistToolTurn(ctx, persistID, toolBody, generationID)
		if cleanMessages != nil {
			*cleanMessages = append(*cleanMessages, bridge.Message{Role: "tool", Content: toolBody})
		}
	}
	visible := strings.TrimSpace(StripCalledToolsMarkers(assistantContent))
	skipNoteOnlyStop := visible == "" && note != "" && call.Name == "sapaloq_stop" && cfg.foregroundOrchestrator
	if assistantContent != "" && !skipNoteOnlyStop {
		o.persistAssistantTurn(ctx, persistID, assistantContent, generationID)
		if cleanMessages != nil {
			*cleanMessages = append(*cleanMessages, bridge.Message{Role: "assistant", Content: assistantContent})
		}
	} else if assistantContent != "" && cleanMessages != nil {
		*cleanMessages = append(*cleanMessages, bridge.Message{Role: "assistant", Content: assistantContent})
	}
	if response != nil {
		response.Reset()
	}
}
