package chat

import (
	"context"
	"strings"
	"time"
)

// PromptSlice is a dynamic system-prompt template indexed from
// prompt/slices/*.md (+ modes/). The assembler reads template_path + conditions
// from this index instead of walking the filesystem per turn.
type PromptSlice struct {
	ID           string `json:"id"`
	Role         string `json:"role"`
	Conditions   string `json:"conditions"`
	TemplatePath string `json:"template_path"`
	TokenBudget  int    `json:"token_budget"`
}

// UpsertPromptSlice inserts or updates a slice keyed on id. conditions defaults
// to "{}" when empty so the column is always valid JSON. A blank id or
// template_path is a no-op.
func (s *Store) UpsertPromptSlice(ctx context.Context, p PromptSlice) error {
	id := strings.TrimSpace(p.ID)
	path := strings.TrimSpace(p.TemplatePath)
	if id == "" || path == "" {
		return nil
	}
	cond := strings.TrimSpace(p.Conditions)
	if cond == "" {
		cond = "{}"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO prompt_slices(id, role, conditions, template_path, token_budget, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			role=excluded.role,
			conditions=excluded.conditions,
			template_path=excluded.template_path,
			token_budget=excluded.token_budget,
			updated_at=excluded.updated_at`,
		id, strings.TrimSpace(p.Role), cond, path, p.TokenBudget, now)
	return err
}

// PromptSlicesForRole returns slices indexed for a role (most recently updated
// first). An empty role returns all slices.
func (s *Store) PromptSlicesForRole(ctx context.Context, role string) ([]PromptSlice, error) {
	q := `SELECT id, role, conditions, template_path, token_budget FROM prompt_slices`
	var args []any
	if r := strings.TrimSpace(role); r != "" {
		q += ` WHERE role=?`
		args = append(args, r)
	}
	q += ` ORDER BY updated_at DESC, id ASC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PromptSlice
	for rows.Next() {
		var p PromptSlice
		if err := rows.Scan(&p.ID, &p.Role, &p.Conditions, &p.TemplatePath, &p.TokenBudget); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
