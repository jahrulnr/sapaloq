package orchestrator

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse/artifacts"
)

func (o *Orchestrator) persistOnResponseDelta(
	ctx context.Context,
	persistID, generationID string,
	cfg turnConfig,
	turnThinking *strings.Builder,
) {
	if !cfg.recordToolTurns {
		return
	}
	o.flushTurnThinking(ctx, persistID, generationID, cfg, turnThinking)
}

func (o *Orchestrator) persistOnStreamEnd(
	ctx context.Context,
	persistID, generationID string,
	cfg turnConfig,
	turnThinking *strings.Builder,
) {
	if !cfg.recordToolTurns {
		return
	}
	o.flushTurnThinking(ctx, persistID, generationID, cfg, turnThinking)
}

func (o *Orchestrator) persistPartialOnCancel(
	ctx context.Context,
	persistID, generationID string,
	cfg turnConfig,
	response string,
	turnThinking *strings.Builder,
) {
	if !cfg.recordToolTurns {
		return
	}
	body := strings.TrimSpace(artifacts.StripModelResponseArtifact(StripCalledToolsMarkers(response)))
	if body != "" && !artifacts.IsAutopilotEcho(body) {
		o.persistAssistantTurn(ctx, persistID, body, generationID)
	}
}

func (o *Orchestrator) persistStopAssistant(
	ctx context.Context,
	persistID, generationID string,
	cfg turnConfig,
	response string,
	pendingTools []scheduledTool,
	trackedTools []toolCallTrace,
) {
	if !cfg.recordToolTurns {
		return
	}
	final := response
	note := calledToolsNoteForTurn(pendingTools, trackedTools)
	if final != "" {
		if note != "" {
			final += "\n\n" + note
		}
		o.persistAssistantTurn(ctx, persistID, final, generationID)
	} else if note != "" {
		o.persistAssistantTurn(ctx, persistID, note, generationID)
	}
}

func (o *Orchestrator) persistContinuationRound(
	ctx context.Context,
	persistID, generationID string,
	cfg turnConfig,
	driver, assistantContent, toolResultsBody string,
	toolResults []string,
	wireMeta json.RawMessage,
) {
	if !cfg.recordToolTurns || o.chat == nil {
		return
	}
	if driver == "gemini-bridge" && len(wireMeta) > 0 {
		o.persistAssistantWireTurn(ctx, persistID, assistantContent, generationID, wireMeta)
	} else if assistantContent != "" {
		o.persistAssistantTurn(ctx, persistID, assistantContent, generationID)
	}
	if len(toolResults) > 0 {
		o.persistToolTurn(ctx, persistID, toolResultsBody, generationID)
		return
	}
	skipPersist := false
	if turns, terr := o.chat.ActiveTurns(ctx, persistID, false); terr == nil {
		for _, t := range turns {
			if t.Role == "autopilot" && t.Content == toolResultsBody {
				skipPersist = true
				break
			}
		}
	}
	if !skipPersist {
		_ = o.chat.AppendAutopilotTurn(ctx, persistID, toolResultsBody, estimateContentTokens(toolResultsBody))
	}
}

func continuationInflight(toolResults []string, toolResultsBody string, wireMeta json.RawMessage) []bridge.Message {
	if len(toolResults) == 0 {
		return []bridge.Message{{Role: "user", Content: toolResultsBody}}
	}
	return nil
}

func appendContinuationWithoutStore(assistantContent, toolResultsBody string, toolResults []string, wireMeta json.RawMessage, cleanMessages []bridge.Message) []bridge.Message {
	if assistantContent != "" || len(wireMeta) > 0 {
		cleanMessages = append(cleanMessages,
			bridge.Message{Role: "assistant", Content: assistantContent, WireMeta: wireMeta},
		)
	}
	contRole := "user"
	if len(toolResults) > 0 {
		contRole = "tool"
	}
	return append(cleanMessages,
		bridge.Message{Role: contRole, Content: toolResultsBody},
	)
}
