package orchestrator

import (
	"context"
	"fmt"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// SessionContextLedger is the single source of truth for how full a session's
// model context is. It merges active turns (replay-shaped), orphan tool results
// from progress JSONL (cursor/codex in-bridge path), per-turn prompt overhead,
// and an optional in-flight message slice for mid-run compaction triggers.
//
// sapaloq:boundary store→orchestrator — ledger unifies turns.json + orch-*.jsonl for usage.
type SessionContextLedger struct {
	SessionID        string
	ContextWindow    int
	TurnTokens       int // active turns, replay-shaped (actorTurnsToMessages)
	StreamToolTokens int // tool results in orch JSONL not covered by role=tool turns
	OverheadTokens   int // system / runtime / prefetch / skills (once per request)
	UsedTokens       int // TurnTokens + StreamToolTokens + OverheadTokens, or live overlay
	Percent          int
	ActiveTurns      int
	CompactedTurns   int
	LastCompactedAt  string
	CheckpointIndex  int // latest checkpoint index, 0 when none
}

// LedgerOptions tunes a ledger build. LiveMessages is the in-flight cleanMessages
// slice during a run; when set, UsedTokens takes max(persisted, estimateMessagesTokens(live))
// without adding overhead twice (live already carries injected system blocks).
type LedgerOptions struct {
	LiveMessages []bridge.Message
}

// SessionContextLedger builds the canonical context budget for a session.
func (o *Orchestrator) SessionContextLedger(ctx context.Context, sessionID string, opts LedgerOptions) (SessionContextLedger, error) {
	if sessionID == "" {
		var err error
		sessionID, err = o.ActiveSession(ctx)
		if err != nil {
			return SessionContextLedger{}, err
		}
	}
	window := o.contextWindow()
	ledger := SessionContextLedger{
		SessionID:     sessionID,
		ContextWindow: window,
	}
	if o.chat == nil {
		return ledger, nil
	}
	turns, err := o.chat.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return ledger, err
	}
	meta, err := o.chat.Usage(ctx, sessionID, o.snapshot().entry.Key, o.snapshot().entry.Model, window)
	if err == nil {
		ledger.ActiveTurns = meta.ActiveTurns
		ledger.CompactedTurns = meta.CompactedTurns
		ledger.LastCompactedAt = meta.LastCompactedAt
	}
	if ck, ckErr := o.chat.LatestCheckpoint(ctx, sessionID); ckErr == nil {
		ledger.CheckpointIndex = ck.Index
	}
	ledger.TurnTokens = activeTurnBodyTokens(turns)
	events, _ := o.readSessionProgressEvents(sessionID)
	ledger.StreamToolTokens = orphanStreamToolTokens(turns, events)
	userMsg := latestUserMessageFromTurns(turns)
	ledger.OverheadTokens = o.estimatePerTurnOverhead(ctx, sessionID, userMsg)
	persisted := ledger.TurnTokens + ledger.StreamToolTokens + ledger.OverheadTokens
	ledger.UsedTokens = persisted
	if len(opts.LiveMessages) > 0 {
		live := estimateMessagesTokens(opts.LiveMessages)
		if live > persisted {
			ledger.UsedTokens = live
		}
	}
	if window > 0 {
		ledger.Percent = (ledger.UsedTokens * 100) / window
	}
	return ledger, nil
}

// Usage projects the ledger into the widget/API chatstore.Usage shape.
func (l SessionContextLedger) Usage(provider, model string) chatstore.Usage {
	return chatstore.Usage{
		SessionID:       l.SessionID,
		UsedTokens:      l.UsedTokens,
		ContextWindow:   l.ContextWindow,
		Percent:         l.Percent,
		Provider:        provider,
		Model:           model,
		CompactedTurns:  l.CompactedTurns,
		ActiveTurns:     l.ActiveTurns,
		LastCompactedAt: l.LastCompactedAt,
	}
}

// activeTurnBodyTokens sizes active turns the same way contextMessages replays them.
func activeTurnBodyTokens(turns []chatstore.Turn) int {
	return estimateMessagesTokens(actorTurnsToMessages(turns))
}

func latestUserMessageFromTurns(turns []chatstore.Turn) string {
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "user" {
			return turns[i].Content
		}
	}
	return ""
}

// orphanStreamToolTokens accounts for tool results that appear in orch-*.jsonl
// (widget + in-bridge cursor/codex tools) but were never persisted as role=tool
// turns — the main undercount that prevented compaction on the cursor path.
func orphanStreamToolTokens(turns []chatstore.Turn, events []bridge.StreamEvent) int {
	gensWithToolTurn := make(map[string]struct{})
	for _, t := range turns {
		if t.Role == "tool" && t.GenerationID != "" {
			gensWithToolTurn[t.GenerationID] = struct{}{}
		}
	}
	seen := make(map[string]struct{})
	total := 0
	for _, ev := range events {
		if ev.Kind != bridge.EventToolUpdate {
			continue
		}
		if ev.GenerationID != "" {
			if _, ok := gensWithToolTurn[ev.GenerationID]; ok {
				continue
			}
		}
		toolID := ""
		if ev.ToolCall != nil {
			toolID = ev.ToolCall.ID
		}
		dedupeKey := fmt.Sprintf("%s|%s", ev.GenerationID, toolID)
		if toolID != "" {
			if _, dup := seen[dedupeKey]; dup {
				continue
			}
			seen[dedupeKey] = struct{}{}
		}
		body := ev.ToolResult
		if body == "" {
			body = ev.Error
		}
		if body == "" {
			continue
		}
		total += estimateContentTokens(body)
	}
	return total
}
