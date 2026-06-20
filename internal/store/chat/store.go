package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const defaultSessionID = "default"

// Turn is a normalized chat turn used for active-session restore and context assembly.
type Turn struct {
	ID                int64      `json:"id"`
	SessionID         string     `json:"session_id"`
	Seq               int        `json:"seq"`
	Role              string     `json:"role"`
	Content           string     `json:"content"`
	TokenEstimate     int        `json:"token_estimate"`
	IncludedInContext bool       `json:"included_in_context"`
	CompactedAt       *time.Time `json:"compacted_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// Usage summarizes active context usage for the widget.
type Usage struct {
	SessionID       string `json:"session_id"`
	UsedTokens      int    `json:"used_tokens"`
	ContextWindow   int    `json:"context_window"`
	Percent         int    `json:"percent"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	CompactedTurns  int    `json:"compacted_turns"`
	ActiveTurns     int    `json:"active_turns"`
	LastCompactedAt string `json:"last_compacted_at,omitempty"`
}

// Store owns companion.db chat/session persistence. JSONL progress remains audit-only.
type Store struct {
	db *sql.DB
	// ftsEnabled reports whether the SQLite build supports FTS5. When false,
	// facts search degrades to a LIKE scan instead of a facts_fts MATCH.
	ftsEnabled bool
}

func Open(memoryDir string) (*Store, error) {
	if memoryDir == "" {
		return nil, errors.New("memory dir is required")
	}
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(memoryDir, "companion.db"))
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE IF NOT EXISTS chat_sessions (
			id TEXT PRIMARY KEY,
			namespace TEXT NOT NULL DEFAULT 'default',
			provider TEXT,
			model TEXT,
			active INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			reset_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS chat_turns (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			token_estimate INTEGER NOT NULL DEFAULT 0,
			included_in_context INTEGER NOT NULL DEFAULT 1,
			compacted_at TEXT,
			created_at TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES chat_sessions(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_chat_turns_session_seq ON chat_turns(session_id, seq)`,
		`CREATE TABLE IF NOT EXISTS chat_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS context_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			used_tokens INTEGER NOT NULL,
			context_window INTEGER NOT NULL,
			percent INTEGER NOT NULL,
			provider TEXT,
			model TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS compaction_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			summary_turn_id INTEGER,
			compacted_turns INTEGER NOT NULL,
			created_at TEXT NOT NULL
		)`,
		// facts is the durable "memory facts" store (canonical schema:
		// migrations/001_initial.sql). It backs do_not_repeat feedback,
		// preferences, skill triggers, and notes. Always created.
		`CREATE TABLE IF NOT EXISTS facts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		// feedback_events records explicit 👍/👎 reward signals from the user;
		// a 👎 with a correction also writes a do_not_repeat fact (see
		// feedback.go). turn_id is nullable for session-level feedback.
		`CREATE TABLE IF NOT EXISTS feedback_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			turn_id INTEGER,
			signal TEXT NOT NULL,
			reward REAL NOT NULL,
			correction TEXT,
			created_at TEXT NOT NULL
		)`,
		// nodes registers local + remote execution targets for sub-agents
		// (see docs/NODES.md). A bootstrapped "local-default" row preserves the
		// existing in-proc spawn behavior. Tokens are NEVER stored here — comm
		// specs reference auth via ENV vars. share_memory is only honored for
		// local nodes (remote always gets a bounded context packet).
		`CREATE TABLE IF NOT EXISTS nodes (
			name TEXT PRIMARY KEY,
			role TEXT NOT NULL DEFAULT '*',
			wrapper TEXT NOT NULL DEFAULT 'local',
			address TEXT NOT NULL DEFAULT '',
			communicate TEXT NOT NULL DEFAULT 'unix',
			comm_spec_path TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			priority INTEGER NOT NULL DEFAULT 0,
			capabilities TEXT NOT NULL DEFAULT '[]',
			share_memory INTEGER NOT NULL DEFAULT 0,
			last_seen_at TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_role ON nodes(role, enabled, priority DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	// FTS5 is optional with the modernc.org/sqlite build. Probe for it; if
	// available, create facts_fts + sync triggers (mirroring
	// migrations/001_initial.sql) so SearchFacts can MATCH. Otherwise leave
	// ftsEnabled false and degrade to a LIKE scan — never hard-fail Open.
	s.ftsEnabled = s.probeFTS5(ctx)
	if s.ftsEnabled {
		ftsStmts := []string{
			`CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(content, content='facts', content_rowid='id')`,
			`CREATE TRIGGER IF NOT EXISTS facts_ai AFTER INSERT ON facts BEGIN
				INSERT INTO facts_fts(rowid, content) VALUES (new.id, new.content);
			END`,
			`CREATE TRIGGER IF NOT EXISTS facts_ad AFTER DELETE ON facts BEGIN
				INSERT INTO facts_fts(facts_fts, rowid, content) VALUES('delete', old.id, old.content);
			END`,
			`CREATE TRIGGER IF NOT EXISTS facts_au AFTER UPDATE ON facts BEGIN
				INSERT INTO facts_fts(facts_fts, rowid, content) VALUES('delete', old.id, old.content);
				INSERT INTO facts_fts(rowid, content) VALUES (new.id, new.content);
			END`,
		}
		for _, stmt := range ftsStmts {
			if _, err := s.db.ExecContext(ctx, stmt); err != nil {
				// FTS5 probe passed but creation failed: degrade gracefully
				// rather than failing Open.
				s.ftsEnabled = false
				break
			}
		}
	}
	return nil
}

// probeFTS5 reports whether the underlying SQLite build supports FTS5. It
// creates a throwaway virtual table inside a transaction and rolls back so no
// schema side effects leak.
func (s *Store) probeFTS5(ctx context.Context) bool {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE VIRTUAL TABLE _fts_probe USING fts5(x)`); err != nil {
		return false
	}
	return true
}

func (s *Store) ActiveSession(ctx context.Context, provider, model string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM chat_sessions WHERE active=1 ORDER BY updated_at DESC LIMIT 1`).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return s.Reset(ctx, provider, model)
}

func (s *Store) Reset(ctx context.Context, provider, model string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := fmt.Sprintf("chat-%d", time.Now().UTC().UnixNano())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE chat_sessions SET active=0, reset_at=?, updated_at=? WHERE active=1`, now, now); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chat_sessions(id, namespace, provider, model, active, created_at, updated_at, reset_at) VALUES (?, 'default', ?, ?, 1, ?, ?, ?)`, id, provider, model, now, now, now); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (s *Store) AppendTurn(ctx context.Context, sessionID, role, content string, tokenEstimate int) error {
	_, err := s.AppendTurnID(ctx, sessionID, role, content, tokenEstimate)
	return err
}

func (s *Store) AppendTurnID(ctx context.Context, sessionID, role, content string, tokenEstimate int) (int64, error) {
	if strings.TrimSpace(content) == "" {
		return 0, nil
	}
	if sessionID == "" {
		sessionID = defaultSessionID
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var seq int
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM chat_turns WHERE session_id=?`, sessionID).Scan(&seq)
	res, err := tx.ExecContext(ctx, `INSERT INTO chat_turns(session_id, seq, role, content, token_estimate, included_in_context, created_at) VALUES (?, ?, ?, ?, ?, 1, ?)`, sessionID, seq, role, content, tokenEstimate, now)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chat_sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) Turn(ctx context.Context, sessionID string, turnID int64) (Turn, error) {
	var t Turn
	var included int
	var compacted sql.NullString
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id, session_id, seq, role, content, token_estimate, included_in_context, compacted_at, created_at
		FROM chat_turns WHERE session_id=? AND id=?`, sessionID, turnID).
		Scan(&t.ID, &t.SessionID, &t.Seq, &t.Role, &t.Content, &t.TokenEstimate, &included, &compacted, &created)
	if err != nil {
		return Turn{}, err
	}
	t.IncludedInContext = included == 1
	if compacted.Valid && compacted.String != "" {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, compacted.String); parseErr == nil {
			t.CompactedAt = &parsed
		}
	}
	if parsed, parseErr := time.Parse(time.RFC3339Nano, created); parseErr == nil {
		t.CreatedAt = parsed
	}
	return t, nil
}

// DeleteFromTurn deletes the selected turn and every later turn in its linear
// branch. Earlier conversation remains intact.
func (s *Store) DeleteFromTurn(ctx context.Context, sessionID string, turnID int64) error {
	return s.deleteRelativeToTurn(ctx, sessionID, turnID, true)
}

// DeleteAfterTurn keeps the selected turn and removes only its descendants.
// Retry uses this so the original user message is regenerated in place.
func (s *Store) DeleteAfterTurn(ctx context.Context, sessionID string, turnID int64) error {
	return s.deleteRelativeToTurn(ctx, sessionID, turnID, false)
}

func (s *Store) deleteRelativeToTurn(ctx context.Context, sessionID string, turnID int64, inclusive bool) error {
	turn, err := s.Turn(ctx, sessionID, turnID)
	if err != nil {
		return err
	}
	op := ">"
	if inclusive {
		op = ">="
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `DELETE FROM chat_turns WHERE session_id=? AND seq ` + op + ` ?`
	if _, err := tx.ExecContext(ctx, query, sessionID, turn.Seq); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chat_sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ActiveTurns(ctx context.Context, sessionID string, includeCompacted bool) ([]Turn, error) {
	query := `SELECT id, session_id, seq, role, content, token_estimate, included_in_context, compacted_at, created_at FROM chat_turns WHERE session_id=?`
	if !includeCompacted {
		query += ` AND included_in_context=1`
	}
	query += ` ORDER BY seq ASC`
	rows, err := s.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Turn
	for rows.Next() {
		var t Turn
		var included int
		var compacted sql.NullString
		var created string
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Seq, &t.Role, &t.Content, &t.TokenEstimate, &included, &compacted, &created); err != nil {
			return nil, err
		}
		t.IncludedInContext = included == 1
		if compacted.Valid && compacted.String != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, compacted.String); err == nil {
				t.CompactedAt = &parsed
			}
		}
		if parsed, err := time.Parse(time.RFC3339Nano, created); err == nil {
			t.CreatedAt = parsed
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Compact(ctx context.Context, sessionID string, keepRecent int, summary string, estimate func(string) int) (int, error) {
	if keepRecent < 2 {
		keepRecent = 2
	}
	turns, err := s.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return 0, err
	}
	if len(turns) <= keepRecent+1 {
		return 0, nil
	}
	cutoff := len(turns) - keepRecent
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, t := range turns[:cutoff] {
		if _, err := tx.ExecContext(ctx, `UPDATE chat_turns SET included_in_context=0, compacted_at=? WHERE id=?`, now, t.ID); err != nil {
			return 0, err
		}
	}
	var seq int
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM chat_turns WHERE session_id=?`, sessionID).Scan(&seq)
	res, err := tx.ExecContext(ctx, `INSERT INTO chat_turns(session_id, seq, role, content, token_estimate, included_in_context, created_at) VALUES (?, ?, 'system', ?, ?, 1, ?)`, sessionID, seq, summary, estimate(summary), now)
	if err != nil {
		return 0, err
	}
	summaryID, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO compaction_runs(session_id, summary_turn_id, compacted_turns, created_at) VALUES (?, ?, ?, ?)`, sessionID, summaryID, cutoff, now); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chat_sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return 0, err
	}
	return cutoff, tx.Commit()
}

func (s *Store) Usage(ctx context.Context, sessionID, provider, model string, contextWindow int) (Usage, error) {
	turns, err := s.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return Usage{}, err
	}
	used := 0
	for _, t := range turns {
		used += t.TokenEstimate
	}
	var compacted int
	var last string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(compacted_turns), 0), COALESCE(MAX(created_at), '') FROM compaction_runs WHERE session_id=?`, sessionID).Scan(&compacted, &last)
	percent := 0
	if contextWindow > 0 {
		percent = (used * 100) / contextWindow
	}
	return Usage{SessionID: sessionID, UsedTokens: used, ContextWindow: contextWindow, Percent: percent, Provider: provider, Model: model, CompactedTurns: compacted, ActiveTurns: len(turns), LastCompactedAt: last}, nil
}

func (s *Store) SnapshotUsage(ctx context.Context, usage Usage) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO context_snapshots(session_id, used_tokens, context_window, percent, provider, model, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, usage.SessionID, usage.UsedTokens, usage.ContextWindow, usage.Percent, usage.Provider, usage.Model, now)
	return err
}
