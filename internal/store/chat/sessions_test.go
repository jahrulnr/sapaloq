package chat

import (
	"context"
	"testing"
	"time"
)

func TestListSessionsOrdersActiveFirstThenRecent(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	// Empty DB returns no sessions (and no error).
	if sessions, err := store.ListSessions(ctx, 50); err != nil {
		t.Fatalf("ListSessions on empty db: %v", err)
	} else if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions on empty db, got %d", len(sessions))
	}

	// Three sessions; Reset deactivates the previous one, so the last Reset is
	// the active session and must sort to the top of the list.
	first, err := store.Reset(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.AppendTurn(ctx, first, "user", "first session question that is fairly long to verify truncation behaviour later", 5)
	_ = store.AppendTurn(ctx, first, "assistant", "answer", 3)

	time.Sleep(2 * time.Millisecond)
	second, err := store.Reset(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.AppendTurn(ctx, second, "user", "second", 2)

	time.Sleep(2 * time.Millisecond)
	third, err := store.Reset(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := store.ListSessions(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}
	if !sessions[0].Active || sessions[0].ID != third {
		t.Fatalf("expected active session %q first, got %#v", third, sessions[0])
	}
	for _, s := range sessions[1:] {
		if s.Active {
			t.Fatalf("only one session may be active, found extra active: %#v", s)
		}
	}

	// Title + turn count derivation.
	var firstSummary *SessionSummary
	for i := range sessions {
		if sessions[i].ID == first {
			firstSummary = &sessions[i]
		}
	}
	if firstSummary == nil {
		t.Fatalf("first session missing from list")
	}
	if firstSummary.TurnCount != 2 {
		t.Fatalf("expected 2 turns for first session, got %d", firstSummary.TurnCount)
	}
	if firstSummary.Title == "" || len([]rune(firstSummary.Title)) > 49 {
		t.Fatalf("title not derived/truncated: %q", firstSummary.Title)
	}
}

func TestListSessionsRespectsLimit(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := store.Reset(ctx, "p", "m"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	sessions, err := store.ListSessions(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected limit of 2, got %d", len(sessions))
	}
}

func TestActivateSwitchesActiveSession(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	first, err := store.Reset(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := store.Reset(ctx, "p", "m") // now active
	if err != nil {
		t.Fatal(err)
	}

	// Switching back to the first session makes it the single active one.
	if err := store.Activate(ctx, first); err != nil {
		t.Fatal(err)
	}
	active, err := store.ActiveSession(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	if active != first {
		t.Fatalf("expected active=%q after Activate, got %q", first, active)
	}

	// Exactly one active session in the index.
	list, err := store.ListSessions(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	activeCount := 0
	for _, s := range list {
		if s.Active {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly 1 active session, got %d", activeCount)
	}
	_ = second
}

func TestActivateRejectsUnknownAndEmpty(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.Reset(ctx, "p", "m"); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "does-not-exist"); err == nil {
		t.Fatal("expected error for unknown session id")
	}
	if err := store.Activate(ctx, "   "); err == nil {
		t.Fatal("expected error for empty session id")
	}
	list, err := store.ListSessions(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	activeCount := 0
	for _, s := range list {
		if s.Active {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("rejected switch must not drop active session, got active count %d", activeCount)
	}
}
