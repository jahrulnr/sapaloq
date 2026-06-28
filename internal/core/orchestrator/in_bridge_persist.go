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

// persistInBridgeToolUpdate records one completed in-bridge MCP tool (cursor/codex
// api5 path) into turns.json and cleanMessages immediately so transport retries,
// context rebuild, and restart see work already done mid-generation.
func (o *Orchestrator) persistInBridgeToolUpdate(
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
	if assistantContent != "" {
		o.persistAssistantTurnWithThinking(ctx, persistID, assistantContent, generationID, cfg, turnThinking)
	} else {
		o.flushTurnThinking(ctx, persistID, generationID, cfg, turnThinking)
	}
	if toolBody != "" {
		_, _ = o.chat.AppendTurnIDWithGeneration(ctx, persistID, "tool", toolBody, estimateContentTokens(toolBody), generationID)
	}
	if assistantContent != "" {
		*cleanMessages = append(*cleanMessages, bridge.Message{Role: "assistant", Content: assistantContent})
	}
	if toolBody != "" {
		*cleanMessages = append(*cleanMessages, bridge.Message{Role: "tool", Content: toolBody})
	}
	if response != nil {
		response.Reset()
	}
	if turnThinking != nil {
		turnThinking.Reset()
	}
}
