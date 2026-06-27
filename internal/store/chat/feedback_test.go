package chat

import (
	"context"
	"testing"
)

func TestAddFeedbackUp(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	turnID := int64(7)
	if err := s.AddFeedback(ctx, "sess", &turnID, "up", ""); err != nil {
		t.Fatalf("add up: %v", err)
	}

	// An "up" must not create any do_not_repeat fact.
	dnr, err := s.RecentDoNotRepeat(ctx, 10)
	if err != nil {
		t.Fatalf("recent dnr: %v", err)
	}
	if len(dnr) != 0 {
		t.Fatalf("expected no do_not_repeat facts after up, got %+v", dnr)
	}

	feedback, err := loadJSONLines[feedbackRecord](s.paths.feedbackFile())
	if err != nil {
		t.Fatalf("load feedback: %v", err)
	}
	count := 0
	for _, fb := range feedback {
		if fb.Signal == "up" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 up event, got %d", count)
	}
}

func TestAddFeedbackDownStoresDoNotRepeat(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.AddFeedback(ctx, "sess", nil, "down", "do not delete files without confirmation"); err != nil {
		t.Fatalf("add down: %v", err)
	}

	dnr, err := s.RecentDoNotRepeat(ctx, 10)
	if err != nil {
		t.Fatalf("recent dnr: %v", err)
	}
	if len(dnr) != 1 {
		t.Fatalf("expected 1 do_not_repeat fact, got %d", len(dnr))
	}
	if dnr[0].Kind != FactKindDoNotRepeat {
		t.Fatalf("expected kind %q, got %q", FactKindDoNotRepeat, dnr[0].Kind)
	}
	if dnr[0].Content != "do not delete files without confirmation" {
		t.Fatalf("unexpected content: %q", dnr[0].Content)
	}
}

func TestAddFeedbackDownNoCorrection(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// A "down" with no correction records the event but no fact.
	if err := s.AddFeedback(ctx, "sess", nil, "down", ""); err != nil {
		t.Fatalf("add down: %v", err)
	}
	dnr, err := s.RecentDoNotRepeat(ctx, 10)
	if err != nil {
		t.Fatalf("recent dnr: %v", err)
	}
	if len(dnr) != 0 {
		t.Fatalf("expected no do_not_repeat fact without correction, got %+v", dnr)
	}
}
