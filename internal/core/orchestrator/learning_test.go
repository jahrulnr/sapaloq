package orchestrator

import (
	"context"
	"testing"
)

func TestDrainLearningQueuePromotesFact(t *testing.T) {
	o := newPrefetchOrch(t)
	ctx := context.Background()

	// Enqueue a promote event; the drain must turn it into a durable fact.
	if _, err := o.chat.EnqueueLearning(ctx, "promote",
		`{"namespace":"personal","kind":"preference","key":"default_notes","value":"personal-notes","content":"default notes target personal-notes","confidence":0.9}`); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// An unknown/malformed event must be skipped (still marked processed) so it
	// can't wedge the queue.
	if _, err := o.chat.EnqueueLearning(ctx, "promote", `{not json}`); err != nil {
		t.Fatalf("enqueue bad: %v", err)
	}

	n, err := o.drainLearningQueue(ctx, 10)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 events drained, got %d", n)
	}

	facts, err := o.chat.FactsByNamespace(ctx, "personal", "preference", 10)
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if len(facts) != 1 || facts[0].Key != "default_notes" || facts[0].Value != "personal-notes" {
		t.Fatalf("expected promoted preference fact, got %+v", facts)
	}

	// Idempotent: a second drain finds nothing pending.
	again, err := o.drainLearningQueue(ctx, 10)
	if err != nil {
		t.Fatalf("drain 2: %v", err)
	}
	if again != 0 {
		t.Fatalf("expected 0 on second drain, got %d", again)
	}
}

func TestSubmitFeedbackDrainsQueue(t *testing.T) {
	o := newPrefetchOrch(t)
	ctx := context.Background()
	// Feedback enabled by default via WithDefaults at the call site.
	o.cfg.Feedback.ExplicitSignalsEnabled = true

	if err := o.chat.AddFeedback(ctx, "s1", nil, "down", "do not overwrite the file"); err != nil {
		t.Fatalf("feedback: %v", err)
	}
	// AddFeedback writes a do_not_repeat fact synchronously AND enqueues a
	// learning event; draining should mark it processed without error.
	n, err := o.drainLearningQueue(ctx, 10)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 feedback event drained, got %d", n)
	}
	dnr, err := o.chat.RecentDoNotRepeat(ctx, 10)
	if err != nil {
		t.Fatalf("dnr: %v", err)
	}
	if len(dnr) != 1 {
		t.Fatalf("expected 1 do_not_repeat fact, got %d", len(dnr))
	}
}
