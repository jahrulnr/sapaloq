package orchestrator

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse/artifacts"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// SessionContext mirrors the persisted rollout/turn store for one active run.
// The turn loop operates on cleanMessages; this type loads the same tail from
// the JSON store on demand so checkpoint anchoring and usage stay aligned.
type SessionContext struct {
	SessionID    string
	GenerationID string
	Turns        []chatstore.Turn
	Messages     []bridge.Message
}

func (o *Orchestrator) loadSessionContext(ctx context.Context, sessionID, generationID string) (SessionContext, error) {
	sc := SessionContext{SessionID: sessionID, GenerationID: generationID}
	if o.chat == nil {
		return sc, nil
	}
	turns, err := o.chat.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return sc, err
	}
	sc.Turns = turns
	return sc, nil
}

func (o *Orchestrator) persistAssistantTurn(ctx context.Context, sessionID, content, generationID string) {
	if o.chat == nil || sessionID == "" {
		return
	}
	content = strings.TrimSpace(artifacts.StripModelResponseArtifact(content))
	if content == "" || artifacts.IsAutopilotEcho(content) {
		return
	}
	_, _ = o.chat.AppendTurnIDWithGeneration(ctx, sessionID, "assistant", content, estimateContentTokens(content), generationID)
}

func (o *Orchestrator) persistAssistantWireTurn(ctx context.Context, sessionID, content, generationID string, wireMeta json.RawMessage) {
	if o.chat == nil || sessionID == "" || len(wireMeta) == 0 {
		return
	}
	content = strings.TrimSpace(artifacts.StripModelResponseArtifact(content))
	if content != "" && artifacts.IsAutopilotEcho(content) {
		content = ""
	}
	_, _ = o.chat.AppendTurnIDWithWireMeta(ctx, sessionID, "assistant", content, estimateContentTokens(content), generationID, wireMeta)
}

func (o *Orchestrator) persistThinkingTurn(ctx context.Context, sessionID, content, generationID string) {
	if o.chat == nil || sessionID == "" {
		return
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	_, _ = o.chat.AppendTurnIDWithGeneration(ctx, sessionID, "thinking", content, 0, generationID)
}

func (o *Orchestrator) flushTurnThinking(ctx context.Context, persistID, generationID string, cfg turnConfig, turnThinking *strings.Builder) {
	if !cfg.recordToolTurns || turnThinking == nil {
		return
	}
	text := strings.TrimSpace(turnThinking.String())
	turnThinking.Reset()
	if text == "" || artifacts.IsThinkingConfabulation(text) {
		return
	}
	if artifacts.IsUnanchoredThinkingConfabulation(text, cfg.taskAnchor) {
		return
	}
	o.persistThinkingTurn(ctx, persistID, text, generationID)
}

func (o *Orchestrator) persistToolTurn(ctx context.Context, sessionID, content, generationID string) {
	if o.chat == nil || sessionID == "" {
		return
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	_, _ = o.chat.AppendTurnIDWithGeneration(ctx, sessionID, "tool", content, estimateContentTokens(content), generationID)
}

// persistSystemPromptAudit writes the composed system stack for one generation
// into turns.json for inspection. Audit-only: IncludedInContext is false so
// replay still injects live system blocks from prompt.go.
func (o *Orchestrator) persistSystemPromptAudit(ctx context.Context, sessionID, generationID string, messages []bridge.Message) {
	if o.chat == nil || sessionID == "" {
		return
	}
	var parts []string
	for _, m := range messages {
		if m.Role != "system" {
			continue
		}
		if body := strings.TrimSpace(m.Content); body != "" {
			parts = append(parts, body)
		}
	}
	if len(parts) == 0 {
		return
	}
	content := strings.Join(parts, "\n\n---\n\n")
	_ = o.chat.DeleteSystemPromptAudits(ctx, sessionID, map[string]struct{}{generationID: {}})
	_, _ = o.chat.AppendTurnIDWithFlags(ctx, sessionID, "system", content, estimateContentTokens(content), generationID, false)
}
