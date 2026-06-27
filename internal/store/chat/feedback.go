package chat

import (
	"context"
	"strings"
	"time"
)

const FactKindDoNotRepeat = "do_not_repeat"

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

	s.mu.Lock()
	id := s.nextFeedbackID
	s.nextFeedbackID++
	s.mu.Unlock()

	rec := feedbackRecord{
		ID: id, SessionID: sessionID, TurnID: turnID, Signal: signal,
		Reward: reward, Correction: correction, CreatedAt: now,
	}
	if err := appendJSONL(s.paths.feedbackFile(), rec); err != nil {
		return err
	}

	if signal == "down" && correction != "" {
		if _, err := s.AddFact(ctx, FactKindDoNotRepeat, correction); err != nil {
			return err
		}
	}
	payload := `{"session_id":` + jsonString(sessionID) +
		`,"signal":` + jsonString(signal) +
		`,"correction":` + jsonString(correction) + `}`
	_, _ = s.EnqueueLearning(ctx, "feedback", payload)
	return nil
}

func jsonString(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	return "\"" + r.Replace(s) + "\""
}

func (s *Store) RecentDoNotRepeat(ctx context.Context, limit int) ([]Fact, error) {
	return s.RecentFacts(ctx, FactKindDoNotRepeat, limit)
}
