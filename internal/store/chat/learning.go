package chat

import (
	"context"
	"strings"
	"time"
)

type LearningEvent struct {
	ID          int64      `json:"id"`
	EventKind   string     `json:"event_kind"`
	Payload     string     `json:"payload"`
	CreatedAt   time.Time  `json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

func (s *Store) EnqueueLearning(ctx context.Context, eventKind, payload string) (int64, error) {
	_ = ctx
	eventKind = strings.TrimSpace(eventKind)
	if eventKind == "" {
		return 0, nil
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		payload = "{}"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextLearningID
	s.nextLearningID++
	now := time.Now().UTC()
	s.learningQueue = append(s.learningQueue, LearningEvent{
		ID: id, EventKind: eventKind, Payload: payload, CreatedAt: now,
	})
	return id, s.saveLearning()
}

func (s *Store) PendingLearning(ctx context.Context, limit int) ([]LearningEvent, error) {
	_ = ctx
	if limit <= 0 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []LearningEvent
	for _, e := range s.learningQueue {
		if e.ProcessedAt != nil {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *Store) MarkLearningProcessed(ctx context.Context, id int64) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for i := range s.learningQueue {
		if s.learningQueue[i].ID == id && s.learningQueue[i].ProcessedAt == nil {
			s.learningQueue[i].ProcessedAt = &now
			return s.saveLearning()
		}
	}
	return nil
}
