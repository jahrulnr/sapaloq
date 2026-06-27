package chat

import (
	"context"
	"testing"
)

func TestFactsAddSearchDelete(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	id, err := s.AddFact(ctx, "preference", "user prefers tabs over spaces")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected non-zero id")
	}
	if _, err := s.AddFact(ctx, "note", "deployment runs on fridays"); err != nil {
		t.Fatalf("add 2: %v", err)
	}

	got, err := s.SearchFacts(ctx, "tabs", nil, 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("expected 1 match for 'tabs', got %+v", got)
	}

	// Kind filter.
	notes, err := s.SearchFacts(ctx, "deployment", []string{"note"}, 10)
	if err != nil {
		t.Fatalf("search note: %v", err)
	}
	if len(notes) != 1 || notes[0].Kind != "note" {
		t.Fatalf("expected 1 note match, got %+v", notes)
	}

	// Delete removes it from search (verifies trigger sync on delete when FTS).
	if err := s.DeleteFact(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	after, err := s.SearchFacts(ctx, "tabs", nil, 10)
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("expected no match after delete, got %+v", after)
	}
}

func TestFactsSearchFallback(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if _, err := s.AddFact(ctx, "note", "the quick brown fox"); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Search always uses substring scan on JSON store.
	got, err := s.SearchFacts(ctx, "brown", nil, 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 LIKE match, got %+v", got)
	}
}

func TestRecentFacts(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	_, _ = s.AddFact(ctx, "do_not_repeat", "old mistake")
	_, _ = s.AddFact(ctx, "note", "a note")
	_, _ = s.AddFact(ctx, "do_not_repeat", "new mistake")

	dnr, err := s.RecentFacts(ctx, "do_not_repeat", 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(dnr) != 2 {
		t.Fatalf("expected 2 do_not_repeat facts, got %d", len(dnr))
	}
	// Most recent first (highest id).
	if dnr[0].Content != "new mistake" {
		t.Fatalf("expected newest first, got %q", dnr[0].Content)
	}
}

// TestFTSQueryIsSafe ensures malformed user text doesn't crash the FTS search
// (it should sanitize or fall back to LIKE).
func TestFTSQueryIsSafe(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.AddFact(ctx, "note", "matching content here"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// FTS operator characters in raw user input must not error.
	for _, q := range []string{`"unterminated`, `AND OR NOT`, `foo) (bar`, `*`} {
		if _, err := s.SearchFacts(ctx, q, nil, 10); err != nil {
			t.Fatalf("search %q errored: %v", q, err)
		}
	}
}
