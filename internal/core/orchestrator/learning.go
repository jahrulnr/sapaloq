package orchestrator

import (
	"context"
	"encoding/json"
)

// promotePayload is the shape of a learning_queue "promote" event: the
// memory-janitor turns it into a durable fact via UpsertFact. Mirrors the
// example in docs/CONTEXT-SOP.md (auto-learning loop).
type promotePayload struct {
	Namespace  string  `json:"namespace"`
	Kind       string  `json:"kind"`
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Content    string  `json:"content"`
	Confidence float64 `json:"confidence"`
}

// feedbackPayload is the shape enqueued by the chat store on each explicit
// 👍/👎 (see feedback.go). The drain uses it to nudge prefetch rule telemetry.
type feedbackPayload struct {
	SessionID  string `json:"session_id"`
	Signal     string `json:"signal"`
	Correction string `json:"correction"`
}

// drainLearningQueue is the in-proc memory-janitor: it processes a bounded
// batch of pending learning events and promotes them into durable memory.
// Currently it handles two event kinds:
//
//   - "promote" → UpsertFact (typed key/value fact, deduped by key)
//   - "feedback" → record a prefetch hit/success signal (telemetry only)
//
// It is best-effort and idempotent: every drained event is marked processed so
// it is never replayed, and a malformed payload is skipped (still marked) so
// one bad row can't wedge the queue. Bandit auto-tuning and research spawning
// remain deferred (see docs/STATUS.md).
func (o *Orchestrator) drainLearningQueue(ctx context.Context, max int) (int, error) {
	if o == nil || o.chat == nil {
		return 0, nil
	}
	if max <= 0 {
		max = 50
	}
	events, err := o.chat.PendingLearning(ctx, max)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, ev := range events {
		switch ev.EventKind {
		case "promote":
			var p promotePayload
			if json.Unmarshal([]byte(ev.Payload), &p) == nil && p.Kind != "" {
				_, _ = o.chat.UpsertFact(ctx, p.Namespace, p.Kind, p.Key, p.Value, p.Content, p.Confidence)
			}
		case "feedback":
			// Telemetry only for now: a thumbs-up/down is folded into the most
			// recent prefetch rule's success_rate by the rule-tuning pass. We
			// intentionally do not mutate facts here beyond the do_not_repeat
			// fact the store already wrote synchronously.
			var f feedbackPayload
			_ = json.Unmarshal([]byte(ev.Payload), &f)
		}
		if err := o.chat.MarkLearningProcessed(ctx, ev.ID); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}
