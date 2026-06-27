package orchestrator

import (
	"context"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
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
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	_, _ = o.chat.AppendTurnIDWithGeneration(ctx, sessionID, "assistant", content, estimateTextTokens(content), generationID)
}
