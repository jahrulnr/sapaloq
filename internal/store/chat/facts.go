package chat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Fact is a durable memory fact: a small, typed, searchable statement the
// orchestrator can store and retrieve (preferences, do_not_repeat penalties,
// notes, skill triggers, …). Backed by the facts table (+ facts_fts when the
// SQLite build supports FTS5).
//
// The Context-SOP fields (Namespace/Key/Value/Confidence) let a fact be
// addressed as a typed, namespaced key/value with a confidence score, while the
// original Content column remains the FTS-searchable text. ObsoleteAt is a
// soft-delete marker: obsolete facts are excluded from search/recent by default.
type Fact struct {
	ID         int64      `json:"id"`
	Namespace  string     `json:"namespace,omitempty"`
	Kind       string     `json:"kind"`
	Key        string     `json:"key,omitempty"`
	Value      string     `json:"value,omitempty"`
	Content    string     `json:"content"`
	Confidence float64    `json:"confidence,omitempty"`
	ObsoleteAt *time.Time `json:"obsolete_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

// factSelectCols is the canonical projection used by every fact query so the
// scan in queryFacts stays in lockstep with the column order.
const factSelectCols = `id, namespace, kind, key, value, content, confidence, obsolete_at, created_at, updated_at`

// defaultNamespace is applied when a caller omits a namespace.
const defaultNamespace = "default"

// AddFact inserts a fact and returns its id. kind and content are required.
// It is the legacy content-only entry point (used by the skill index and the
// do_not_repeat feedback path); namespace defaults to "default" and the typed
// key/value columns are left null.
func (s *Store) AddFact(ctx context.Context, kind, content string) (int64, error) {
	kind = strings.TrimSpace(kind)
	content = strings.TrimSpace(content)
	if kind == "" || content == "" {
		return 0, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO facts(namespace, kind, content, confidence, created_at, updated_at) VALUES (?, ?, ?, 1.0, ?, ?)`,
		defaultNamespace, kind, content, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpsertFact stores a typed, namespaced key/value fact, deduping on
// (namespace, kind, key): an existing live fact with the same key is updated in
// place (value/content/confidence refreshed, obsolete_at cleared) rather than
// duplicated. When key is empty it always inserts (free-form note). content is
// what FTS searches; if empty it is derived from key/value so the fact is still
// discoverable. Returns the row id.
func (s *Store) UpsertFact(ctx context.Context, namespace, kind, key, value, content string, confidence float64) (int64, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	content = strings.TrimSpace(content)
	if kind == "" {
		return 0, nil
	}
	if content == "" {
		content = strings.TrimSpace(strings.TrimSpace(key + " " + value))
	}
	if content == "" {
		return 0, nil
	}
	if confidence <= 0 {
		confidence = 1.0
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if key != "" {
		var id int64
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM facts WHERE namespace=? AND kind=? AND key=? AND obsolete_at IS NULL ORDER BY id DESC LIMIT 1`,
			namespace, kind, key).Scan(&id)
		switch {
		case err == nil:
			if _, uerr := s.db.ExecContext(ctx,
				`UPDATE facts SET value=?, content=?, confidence=?, obsolete_at=NULL, updated_at=? WHERE id=?`,
				value, content, confidence, now, id); uerr != nil {
				return 0, uerr
			}
			return id, nil
		case errors.Is(err, sql.ErrNoRows):
			// fall through to insert
		default:
			return 0, err
		}
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO facts(namespace, kind, key, value, content, confidence, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		namespace, kind, nullable(key), nullable(value), content, confidence, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ObsoleteFact soft-deletes a fact by id: it is stamped with obsolete_at and
// excluded from future search/recent results, but the row (and its FTS shadow)
// remains for audit. A no-op for an unknown id.
func (s *Store) ObsoleteFact(ctx context.Context, id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE facts SET obsolete_at=?, updated_at=? WHERE id=? AND obsolete_at IS NULL`, now, now, id)
	return err
}

// FactsByNamespace returns live facts in a namespace, newest first, optionally
// filtered by kind. Obsolete facts are excluded. Used by the prefetch pipeline
// to load mode-scoped preferences/routines without an FTS query.
func (s *Store) FactsByNamespace(ctx context.Context, namespace, kind string, limit int) ([]Fact, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}
	if limit <= 0 {
		limit = 20
	}
	sqlStr := `SELECT ` + factSelectCols + ` FROM facts WHERE namespace=? AND obsolete_at IS NULL`
	args := []any{namespace}
	if k := strings.TrimSpace(kind); k != "" {
		sqlStr += ` AND kind=?`
		args = append(args, k)
	}
	sqlStr += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return s.queryFacts(ctx, sqlStr, args...)
}

// nullable converts an empty string to a NULL so the typed columns stay null
// rather than storing "".
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
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
	sql := `SELECT f.id, f.namespace, f.kind, f.key, f.value, f.content, f.confidence, f.obsolete_at, f.created_at, f.updated_at
		FROM facts_fts ft JOIN facts f ON f.id = ft.rowid
		WHERE facts_fts MATCH ? AND f.obsolete_at IS NULL`
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
	sql := `SELECT ` + factSelectCols + ` FROM facts WHERE content LIKE '%' || ? || '%' AND obsolete_at IS NULL`
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
	sql := `SELECT ` + factSelectCols + ` FROM facts WHERE obsolete_at IS NULL`
	var args []any
	if strings.TrimSpace(kind) != "" {
		sql += " AND kind = ?"
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

func (s *Store) queryFacts(ctx context.Context, query string, args ...any) ([]Fact, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		var f Fact
		var (
			namespace  sql.NullString
			key        sql.NullString
			value      sql.NullString
			confidence sql.NullFloat64
			obsolete   sql.NullString
			created    string
			updated    sql.NullString
		)
		if err := rows.Scan(&f.ID, &namespace, &f.Kind, &key, &value, &f.Content, &confidence, &obsolete, &created, &updated); err != nil {
			return nil, err
		}
		f.Namespace = namespace.String
		f.Key = key.String
		f.Value = value.String
		if confidence.Valid {
			f.Confidence = confidence.Float64
		}
		f.CreatedAt = parseFactTime(created)
		if obsolete.Valid && obsolete.String != "" {
			if t := parseFactTime(obsolete.String); !t.IsZero() {
				f.ObsoleteAt = &t
			}
		}
		if updated.Valid && updated.String != "" {
			if t := parseFactTime(updated.String); !t.IsZero() {
				f.UpdatedAt = &t
			}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// parseFactTime accepts both the RFC3339Nano format written by the store and
// the "YYYY-MM-DD HH:MM:SS" format produced by SQLite's CURRENT_TIMESTAMP
// default (legacy rows). Returns the zero time when neither parses.
func parseFactTime(s string) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return parsed
	}
	if parsed, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return parsed
	}
	return time.Time{}
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
