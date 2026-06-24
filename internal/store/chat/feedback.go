package chat

import (
	"context"
	"strings"
	"time"
)

// FactKindDoNotRepeat is the fact kind used for user-flagged mistakes that
// should be injected as negative guidance into future prompts.
const FactKindDoNotRepeat = "do_not_repeat"

// AddFeedback records an explicit reward signal for an assistant turn. signal
// is "up" or "down" (reward +1 / -1). turnID is optional (nil → session-level).
// On a "down" with a non-empty correction, it also stores a do_not_repeat fact
// so the mistake can be surfaced as negative guidance next turn.
func (s *Store) AddFeedback(ctx context.Context, sessionID string, turnID *int64, signal, correction string) error {
	if sessionID == "" {
		sessionID = defaultSessionID
	}
	signal = strings.ToLower(strings.TrimSpace(signal))
	reward := 0.0
	switch signal {
	case "up":
		reward = 1
	case "down":
		reward = -1
	default:
		signal = "up"
		reward = 1
	}
	correction = strings.TrimSpace(correction)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var turnVal any
	if turnID != nil {
		turnVal = *turnID
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO feedback_events(session_id, turn_id, signal, reward, correction, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, turnVal, signal, reward, nullableString(correction), now); err != nil {
		return err
	}

	if signal == "down" && correction != "" {
		if _, err := s.AddFact(ctx, FactKindDoNotRepeat, correction); err != nil {
			return err
		}
	}

	// Queue a learning event so the async drain (memory-janitor) can act on the
	// reward signal later — e.g. tune a prefetch rule's success_rate or build an
	// overlay. Best-effort: a queue failure must not fail the feedback write the
	// user already saw acknowledged.
	payload := `{"session_id":` + jsonString(sessionID) +
		`,"signal":` + jsonString(signal) +
		`,"correction":` + jsonString(correction) + `}`
	_, _ = s.EnqueueLearning(ctx, "feedback", payload)
	return nil
}

// jsonString renders a Go string as a minimal JSON string literal for the small
// hand-built payloads above (avoids pulling encoding/json into this file for a
// few fields).
func jsonString(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	return "\"" + r.Replace(s) + "\""
}

// RecentDoNotRepeat returns the most recent do_not_repeat facts, used to inject
// bounded negative guidance into the Ask prompt.
func (s *Store) RecentDoNotRepeat(ctx context.Context, limit int) ([]Fact, error) {
	return s.RecentFacts(ctx, FactKindDoNotRepeat, limit)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
