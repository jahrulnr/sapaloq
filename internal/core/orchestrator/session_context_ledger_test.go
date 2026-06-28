package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestSessionContextLedgerCountsOrphanStreamTools(t *testing.T) {
	ctx := context.Background()
	memDir := t.TempDir()
	progressDir := filepath.Join(memDir, "state", "rollout")
	if err := os.MkdirAll(progressDir, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := chatstore.Open(memDir)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "cursor", "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(ctx, sessionID, "user", "check the site", 10); err != nil {
		t.Fatal(err)
	}
	gen := "42"
	if _, err := store.AppendTurnIDWithGeneration(ctx, sessionID, "assistant", "on it", 5, gen); err != nil {
		t.Fatal(err)
	}
	bigResult := strings.Repeat("x", 8000)
	progressPath := filepath.Join(progressDir, "orch-"+sessionID+".jsonl")
	ev := map[string]any{
		"kind": "tool_update", "session_id": sessionID, "generation_id": gen,
		"tool_call": map[string]string{"id": "t1", "name": "mcp_read"},
		"tool_result": bigResult,
		"at":          time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, _ := json.Marshal(ev)
	if err := os.WriteFile(progressPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{
		chat:     store,
		cfg:      config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		progress: newAsyncProgressWriter(ProgressWriter{Dir: progressDir}),
	}
	without, err := o.SessionContextLedger(ctx, sessionID, LedgerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if without.StreamToolTokens < 1500 {
		t.Fatalf("StreamToolTokens = %d, want large orphan tool body counted", without.StreamToolTokens)
	}
	if _, err := store.AppendTurnIDWithGeneration(ctx, sessionID, "tool", bigResult, estimateContentTokens(bigResult), gen); err != nil {
		t.Fatal(err)
	}
	with, err := o.SessionContextLedger(ctx, sessionID, LedgerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if with.StreamToolTokens != 0 {
		t.Fatalf("StreamToolTokens = %d after role=tool persist, want 0 (no double count)", with.StreamToolTokens)
	}
}

func TestSessionContextLedgerMatchesReplayNotRawAutopilot(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	autoBody := "<sapaloq:autopilot>continue</sapaloq:autopilot>"
	if err := store.AppendAutopilotTurn(ctx, sessionID, autoBody, estimateContentTokens(autoBody)); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{chat: store, cfg: config.Config{Orchestrator: config.DefaultOrchestratorConfig()}}
	ledger, err := o.SessionContextLedger(ctx, sessionID, LedgerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.TurnTokens != 0 {
		t.Fatalf("TurnTokens = %d, want 0 — autopilot is not replayed to the model", ledger.TurnTokens)
	}
}

func TestEffectiveContextPercentNoDoubleOverhead(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{chat: store, cfg: config.Config{Orchestrator: config.DefaultOrchestratorConfig()}}
	live, err := o.buildForegroundActorMessages(ctx, sessionID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	const window = 200_000
	pct := o.effectiveContextPercent(ctx, sessionID, live, window)
	liveOnly := (estimateMessagesTokens(live) * 100) / window
	if pct > liveOnly+2 {
		t.Fatalf("effective = %d%%, live-only = %d%%; overhead must not be double-counted", pct, liveOnly)
	}
	usage, err := o.ContextUsage(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Percent > liveOnly+2 {
		t.Fatalf("ContextUsage = %d%%, live-only = %d%%; pill should match ledger persisted path", usage.Percent, liveOnly)
	}
}
