package chat

import (
	"context"
	"strings"
	"time"
)

// Fact is a durable memory fact: a small, typed, searchable statement the
// orchestrator can store and retrieve (preferences, do_not_repeat penalties,
// notes, skill triggers, …). Backed by the facts table (+ facts_fts when the
// SQLite build supports FTS5).
type Fact struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// AddFact inserts a fact and returns its id. kind and content are required.
func (s *Store) AddFact(ctx context.Context, kind, content string) (int64, error) {
	kind = strings.TrimSpace(kind)
	content = strings.TrimSpace(content)
	if kind == "" || content == "" {
		return 0, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `INSERT INTO facts(kind, content, created_at) VALUES (?, ?, ?)`, kind, content, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SearchFacts returns facts matching query, most relevant first. It uses an
// FTS5 MATCH when available, falling back to a LIKE scan otherwise (or when the
// FTS query is rejected as malformed). An optional kinds filter restricts the
// result to those kinds.
func (s *Store) SearchFacts(ctx context.Context, query string, kinds []string, limit int) ([]Fact, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return s.RecentFacts(ctx, "", limit)
	}
	if limit <= 0 {
		limit = 20
	}

	if s.ftsEnabled {
		facts, err := s.searchFactsFTS(ctx, query, kinds, limit)
		if err == nil {
			return facts, nil
		}
		// Malformed MATCH expression (or any FTS error): degrade to LIKE.
	}
	return s.searchFactsLike(ctx, query, kinds, limit)
}

func (s *Store) searchFactsFTS(ctx context.Context, query string, kinds []string, limit int) ([]Fact, error) {
	sql := `SELECT f.id, f.kind, f.content, f.created_at
		FROM facts_fts ft JOIN facts f ON f.id = ft.rowid
		WHERE facts_fts MATCH ?`
	args := []any{ftsQuery(query)}
	if clause, kindArgs := kindFilter("f.kind", kinds); clause != "" {
		sql += " AND " + clause
		args = append(args, kindArgs...)
	}
	sql += " ORDER BY rank LIMIT ?"
	args = append(args, limit)
	return s.queryFacts(ctx, sql, args...)
}

func (s *Store) searchFactsLike(ctx context.Context, query string, kinds []string, limit int) ([]Fact, error) {
	sql := `SELECT id, kind, content, created_at FROM facts WHERE content LIKE '%' || ? || '%'`
	args := []any{query}
	if clause, kindArgs := kindFilter("kind", kinds); clause != "" {
		sql += " AND " + clause
		args = append(args, kindArgs...)
	}
	sql += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)
	return s.queryFacts(ctx, sql, args...)
}

// RecentFacts returns the most recent facts, optionally filtered by kind.
func (s *Store) RecentFacts(ctx context.Context, kind string, limit int) ([]Fact, error) {
	if limit <= 0 {
		limit = 20
	}
	sql := `SELECT id, kind, content, created_at FROM facts`
	var args []any
	if strings.TrimSpace(kind) != "" {
		sql += " WHERE kind = ?"
		args = append(args, strings.TrimSpace(kind))
	}
	sql += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)
	return s.queryFacts(ctx, sql, args...)
}

// DeleteFact removes a fact by id. The facts_ad trigger keeps facts_fts in
// sync when FTS5 is enabled.
func (s *Store) DeleteFact(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM facts WHERE id = ?`, id)
	return err
}

func (s *Store) queryFacts(ctx context.Context, sql string, args ...any) ([]Fact, error) {
	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		var f Fact
		var created string
		if err := rows.Scan(&f.ID, &f.Kind, &f.Content, &created); err != nil {
			return nil, err
		}
		if parsed, perr := time.Parse(time.RFC3339Nano, created); perr == nil {
			f.CreatedAt = parsed
		} else if parsed, perr := time.Parse("2006-01-02 15:04:05", created); perr == nil {
			// CURRENT_TIMESTAMP default format.
			f.CreatedAt = parsed
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// kindFilter builds a `col IN (?, ?, …)` clause for a non-empty kinds slice.
func kindFilter(col string, kinds []string) (string, []any) {
	var clean []any
	var placeholders []string
	for _, k := range kinds {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		clean = append(clean, k)
		placeholders = append(placeholders, "?")
	}
	if len(clean) == 0 {
		return "", nil
	}
	return col + " IN (" + strings.Join(placeholders, ", ") + ")", clean
}

// ftsQuery sanitizes a free-text query into a safe FTS5 MATCH expression: each
// whitespace-separated token is double-quoted (escaping embedded quotes) and
// OR-joined, so arbitrary user text can't produce a syntax error or inject FTS
// operators. Returns a harmless token when the input has no usable terms.
func ftsQuery(query string) string {
	fields := strings.Fields(query)
	var terms []string
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, `""`)
		if f == `""` || f == "" {
			continue
		}
		terms = append(terms, `"`+f+`"`)
	}
	if len(terms) == 0 {
		return `""`
	}
	return strings.Join(terms, " OR ")
}
