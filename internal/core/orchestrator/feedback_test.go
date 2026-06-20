package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// TestSubmitFeedbackDisabledIsNoop verifies that when explicit signals are off,
// SubmitFeedback writes nothing (so the widget can call it unconditionally).
func TestSubmitFeedbackDisabledIsNoop(t *testing.T) {
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	o := &Orchestrator{chat: store, cfg: config.Config{Feedback: config.FeedbackConfig{ExplicitSignalsEnabled: false}}}
	ctx := context.Background()

	if err := o.SubmitFeedback(ctx, "sess", 1, "down", "should be ignored"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	dnr, err := store.RecentDoNotRepeat(ctx, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(dnr) != 0 {
		t.Fatalf("expected no facts when disabled, got %+v", dnr)
	}
}

// TestSubmitFeedbackEnabledStoresGuidance verifies the enabled path persists a
// do_not_repeat fact, and that negativeGuidanceBlock surfaces it bounded by the
// configured maxNegativeSlicesPerTurn.
func TestSubmitFeedbackEnabledStoresGuidance(t *testing.T) {
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	o := &Orchestrator{chat: store, cfg: config.Config{Feedback: config.FeedbackConfig{ExplicitSignalsEnabled: true, MaxNegativeSlicesPerTurn: 1}}}
	ctx := context.Background()

	if err := o.SubmitFeedback(ctx, "sess", 0, "down", "mistake one"); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	if err := o.SubmitFeedback(ctx, "sess", 0, "down", "mistake two"); err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	block := o.negativeGuidanceBlock(ctx)
	if block == "" {
		t.Fatalf("expected non-empty guidance block")
	}
	// Bounded to 1 slice → only the most recent mistake appears.
	if strings.Count(block, "\n- ") != 1 {
		t.Fatalf("expected exactly 1 bounded slice, got block: %q", block)
	}
	if !strings.Contains(block, "mistake two") {
		t.Fatalf("expected most-recent mistake in block, got: %q", block)
	}
}
