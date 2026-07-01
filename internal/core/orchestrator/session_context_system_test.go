package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestPersistSystemPromptAuditBeforeUserTurn(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := chatstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "chat-order"
	o := &Orchestrator{chat: store}

	messages := []bridge.Message{
		{Role: "system", Content: "orchestrator stack"},
		{Role: "user", Content: "hai"},
	}
	o.persistSystemPromptAudit(ctx, sessionID, "2", messages)
	_, _ = store.AppendTurnIDWithGeneration(ctx, sessionID, "user", "hai", 1, "2")

	all, err := store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(all))
	}
	if all[0].Role != "system" || all[1].Role != "user" {
		t.Fatalf("order = %s then %s, want system then user", all[0].Role, all[1].Role)
	}
	if all[0].Seq >= all[1].Seq {
		t.Fatalf("system seq %d must precede user seq %d", all[0].Seq, all[1].Seq)
	}
}

func TestPersistSystemPromptAuditExcludedFromReplay(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := chatstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "chat-audit"
	o := &Orchestrator{chat: store}

	messages := []bridge.Message{
		{Role: "system", Content: "persona+rules+orchestrator"},
		{Role: "system", Content: "runtime paths"},
		{Role: "user", Content: "hello"},
	}
	o.persistSystemPromptAudit(ctx, sessionID, "1", messages)

	all, err := store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Role != "system" {
		t.Fatalf("expected one system audit turn, got %+v", all)
	}
	if all[0].IncludedInContext {
		t.Fatal("system audit turn must not be included in context")
	}
	if !strings.Contains(all[0].Content, "persona+rules+orchestrator") || !strings.Contains(all[0].Content, "runtime paths") {
		t.Fatalf("unexpected audit content: %q", all[0].Content)
	}

	active, err := store.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("audit system turn must not appear in active replay set, got %d", len(active))
	}
	replay := actorTurnsToMessages(active)
	for _, m := range replay {
		if m.Role == "system" {
			t.Fatalf("replay must not contain audit system message: %+v", replay)
		}
	}
}
