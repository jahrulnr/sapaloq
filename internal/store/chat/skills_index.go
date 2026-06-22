package chat

import (
	"context"
	"strings"
	"time"
)

// SkillIndexEntry is one row of the SapaLOQ-local skills registry: the id, its
// trigger phrases (stored as a JSON array), the on-disk path, an optional token
// cap, and a priority. Populated at boot from skills/*.md so the assembler can
// resolve a skill by id without re-reading every file.
type SkillIndexEntry struct {
	ID        string `json:"id"`
	Triggers  string `json:"triggers"`
	Path      string `json:"path"`
	MaxTokens int    `json:"max_tokens"`
	Priority  int    `json:"priority"`
}

// UpsertSkillIndex inserts or updates a skill registry row keyed on id. A blank
// id is a no-op; triggers defaults to "[]" so the column is always valid JSON.
func (s *Store) UpsertSkillIndex(ctx context.Context, e SkillIndexEntry) error {
	id := strings.TrimSpace(e.ID)
	if id == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO skills_index(id, triggers, path, max_tokens, priority, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			triggers=excluded.triggers,
			path=excluded.path,
			max_tokens=excluded.max_tokens,
			priority=excluded.priority,
			updated_at=excluded.updated_at`,
		id, jsonOrEmptyArray(e.Triggers), strings.TrimSpace(e.Path), e.MaxTokens, e.Priority, now)
	return err
}

// SkillIndexEntries returns the full skills registry ordered by priority
// (highest first) then id, so the prefetch pipeline can pick the top matches.
func (s *Store) SkillIndexEntries(ctx context.Context) ([]SkillIndexEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, triggers, path, max_tokens, priority FROM skills_index ORDER BY priority DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SkillIndexEntry
	for rows.Next() {
		var e SkillIndexEntry
		if err := rows.Scan(&e.ID, &e.Triggers, &e.Path, &e.MaxTokens, &e.Priority); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
