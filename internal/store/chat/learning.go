package chat

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// LearningEvent is one async learning item: a memory-janitor drains pending
// rows (processed_at IS NULL) and promotes them into facts / prefetch_rules /
// overlays. Payload is opaque JSON owned by the orchestrator.
type LearningEvent struct {
	ID          int64      `json:"id"`
	EventKind   string     `json:"event_kind"`
	Payload     string     `json:"payload"`
	CreatedAt   time.Time  `json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

// EnqueueLearning appends a pending learning event and returns its id. A blank
// kind is a no-op (returns 0). Empty payload is stored as "{}".
func (s *Store) EnqueueLearning(ctx context.Context, eventKind, payload string) (int64, error) {
	eventKind = strings.TrimSpace(eventKind)
	if eventKind == "" {
		return 0, nil
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		payload = "{}"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO learning_queue(event_kind, payload, created_at) VALUES (?, ?, ?)`,
		eventKind, payload, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// PendingLearning returns up to limit unprocessed events, oldest first, so the
// janitor drains them in arrival order.
func (s *Store) PendingLearning(ctx context.Context, limit int) ([]LearningEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event_kind, payload, created_at, processed_at FROM learning_queue
		 WHERE processed_at IS NULL ORDER BY id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LearningEvent
	for rows.Next() {
		var (
			e         LearningEvent
			created   string
			processed sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.EventKind, &e.Payload, &created, &processed); err != nil {
			return nil, err
		}
		e.CreatedAt = parseFactTime(created)
		if processed.Valid && processed.String != "" {
			if t := parseFactTime(processed.String); !t.IsZero() {
				e.ProcessedAt = &t
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkLearningProcessed stamps an event processed so it is not drained again.
// Idempotent: re-marking an already-processed row is a no-op.
func (s *Store) MarkLearningProcessed(ctx context.Context, id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE learning_queue SET processed_at=? WHERE id=? AND processed_at IS NULL`, now, id)
	return err
}
