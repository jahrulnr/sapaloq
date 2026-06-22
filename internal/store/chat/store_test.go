package chat

import (
	"context"
	"testing"
)

func TestDeleteFromTurnKeepsEarlierConversation(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, err := store.Reset(ctx, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := store.AppendTurnID(ctx, sessionID, "user", "first", 1)
	_ = store.AppendTurn(ctx, sessionID, "assistant", "first answer", 1)
	second, _ := store.AppendTurnID(ctx, sessionID, "user", "second", 1)
	_ = store.AppendTurn(ctx, sessionID, "assistant", "second answer", 1)

	if err := store.DeleteFromTurn(ctx, sessionID, second); err != nil {
		t.Fatal(err)
	}
	turns, err := store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].ID != first || turns[1].Content != "first answer" {
		t.Fatalf("unexpected remaining turns: %#v", turns)
	}
}

func TestDeleteAfterTurnSupportsRetryInPlace(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, err := store.Reset(ctx, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := store.AppendTurnID(ctx, sessionID, "user", "retry me", 1)
	_ = store.AppendTurn(ctx, sessionID, "assistant", "bad answer", 1)

	if err := store.DeleteAfterTurn(ctx, sessionID, userID); err != nil {
		t.Fatal(err)
	}
	turns, err := store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].ID != userID || turns[0].Content != "retry me" {
		t.Fatalf("retry changed original user turn: %#v", turns)
	}
}

func TestUsageCountsAllRolesIncludingToolTurns(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, err := store.Reset(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	// A realistic turn: user asks, a tool runs (results sent to the model), then
	// the assistant answers. The tool results are real context tokens and must
	// be counted — this is the regression that made usage read far too low.
	_ = store.AppendTurn(ctx, sessionID, "user", "baca /etc/hosts", 5)
	_ = store.AppendTurn(ctx, sessionID, "tool", "[Tool results]\n127.0.0.1 localhost ...", 40)
	_ = store.AppendTurn(ctx, sessionID, "assistant", "Ini isi /etc/hosts ...", 12)

	usage, err := store.Usage(ctx, sessionID, "p", "m", 1000000)
	if err != nil {
		t.Fatal(err)
	}
	if usage.UsedTokens != 57 { // 5 + 40 + 12 — the tool turn is NOT free
		t.Fatalf("expected used=57 (incl. tool turn), got %d", usage.UsedTokens)
	}
	if usage.ActiveTurns != 3 {
		t.Fatalf("expected 3 active turns, got %d", usage.ActiveTurns)
	}
}
