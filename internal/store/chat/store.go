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
	// CheckpointIndex is 0/NULL for a normal turn, or N>0 when this turn is a
	// checkpoint marker (role=checkpoint) created by the Nth compaction. The UI
	// renders a "Checkpoint N" divider at these turns; the context assembler
	// replays only the latest checkpoint summary + anchored tail.
	CheckpointIndex int `json:"checkpoint_index,omitempty"`
	// GenerationID links a turn to the chat run (runSeq) that produced it.
	GenerationID string    `json:"generation_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
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
		// existing in-proc spawn behavior. Tokens are NEVER stored here - comm
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
		// skills_index is the SapaLOQ-local skills registry (id → triggers/path/
		// max_tokens). Populated at boot from skills/*.md so the assembler reads
		// paths from the index rather than walking the filesystem per turn.
		`CREATE TABLE IF NOT EXISTS skills_index (
			id TEXT PRIMARY KEY,
			triggers TEXT NOT NULL DEFAULT '[]',
			path TEXT NOT NULL DEFAULT '',
			max_tokens INTEGER NOT NULL DEFAULT 0,
			priority INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		)`,
		// prefetch_rules maps an intent to the fact kinds / skills / config keys
		// the ingress pipeline should prefetch, plus bandit-style telemetry
		// (hit_count/success_rate) that drives rule tuning.
		`CREATE TABLE IF NOT EXISTS prefetch_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			intent_pattern TEXT NOT NULL,
			namespace TEXT NOT NULL DEFAULT 'default',
			fact_kinds TEXT NOT NULL DEFAULT '[]',
			skill_ids TEXT NOT NULL DEFAULT '[]',
			config_keys TEXT NOT NULL DEFAULT '[]',
			hit_count INTEGER NOT NULL DEFAULT 0,
			success_count INTEGER NOT NULL DEFAULT 0,
			success_rate REAL NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_prefetch_rules_intent ON prefetch_rules(intent_pattern, namespace)`,
		// prompt_slices indexes dynamic system-prompt templates (role +
		// conditions + template_path), populated from prompt/slices/*.md at boot.
		`CREATE TABLE IF NOT EXISTS prompt_slices (
			id TEXT PRIMARY KEY,
			role TEXT NOT NULL DEFAULT '',
			conditions TEXT NOT NULL DEFAULT '{}',
			template_path TEXT NOT NULL,
			token_budget INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		)`,
		// learning_queue holds async learning events drained by the
		// memory-janitor; rows with processed_at IS NULL are pending.
		`CREATE TABLE IF NOT EXISTS learning_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_kind TEXT NOT NULL,
			payload TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			processed_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_learning_queue_unprocessed ON learning_queue(processed_at, id)`,
		// hot_cache is an optional restart warm-up / repeat-within-5-min serve,
		// bounded by expires_at (expired rows pruned lazily on read).
		`CREATE TABLE IF NOT EXISTS hot_cache (
			cache_key TEXT PRIMARY KEY,
			payload TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		// prefetch_log is telemetry for prefetch rule tuning - one row per ingress.
		`CREATE TABLE IF NOT EXISTS prefetch_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL DEFAULT '',
			intent TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			deep_check_used INTEGER NOT NULL DEFAULT 0,
			task_success INTEGER,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	// Context-SOP memory columns on facts (additive). The original schema was
	// (kind, content, created_at); these let a fact be addressed as a typed,
	// namespaced key/value with confidence + soft-delete (obsolete_at). An
	// existing DB created before this change is upgraded here idempotently;
	// a fresh DB also lands here (the CREATE TABLE above is the bare schema).
	factCols := []struct{ name, ddl string }{
		{"namespace", "namespace TEXT NOT NULL DEFAULT 'default'"},
		{"key", "key TEXT"},
		{"value", "value TEXT"},
		{"confidence", "confidence REAL NOT NULL DEFAULT 1.0"},
		{"obsolete_at", "obsolete_at TEXT"},
		{"updated_at", "updated_at TEXT"},
	}
	for _, c := range factCols {
		if err := s.addColumnIfMissing(ctx, "facts", c.name, c.ddl); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_facts_namespace_kind ON facts(namespace, kind, obsolete_at)`,
		`CREATE INDEX IF NOT EXISTS idx_facts_key ON facts(namespace, kind, key)`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	// LLM-driven checkpoint compaction (migrations/002_checkpoints.sql). The
	// original compaction schema stored a single summary turn + a compaction_runs
	// row; the checkpoint model adds a monotonic per-session checkpoint_index and
	// a reason, and marks the checkpoint turn itself with that index so the UI
	// can render a "Checkpoint n" divider and the context assembler can replay
	// only the latest checkpoint + anchored tail. Additive ALTERs upgrade an
	// existing DB idempotently; a fresh DB also lands here.
	ckptTurnCols := []struct{ name, ddl string }{
		{"checkpoint_index", "checkpoint_index INTEGER"},
	}
	for _, c := range ckptTurnCols {
		if err := s.addColumnIfMissing(ctx, "chat_turns", c.name, c.ddl); err != nil {
			return err
		}
	}
	ckptRunCols := []struct{ name, ddl string }{
		{"checkpoint_index", "checkpoint_index INTEGER"},
		{"reason", "reason TEXT NOT NULL DEFAULT ''"},
		{"tail_start_turn_id", "tail_start_turn_id INTEGER"},
	}
	for _, c := range ckptRunCols {
		if err := s.addColumnIfMissing(ctx, "compaction_runs", c.name, c.ddl); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_chat_turns_checkpoint ON chat_turns(session_id, checkpoint_index)`,
		`CREATE INDEX IF NOT EXISTS idx_compaction_runs_session_idx ON compaction_runs(session_id, checkpoint_index)`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := s.addColumnIfMissing(ctx, "chat_turns", "generation_id", "generation_id TEXT"); err != nil {
		return err
	}

	// FTS5 is optional with the modernc.org/sqlite build. Probe for it; if
	// available, create facts_fts + sync triggers (mirroring
	// migrations/001_initial.sql) so SearchFacts can MATCH. Otherwise leave
	// ftsEnabled false and degrade to a LIKE scan - never hard-fail Open.
	s.ftsEnabled = s.probeFTS5(ctx)
	if s.ftsEnabled {
		// Whether facts_fts already existed before this Open. When it didn't but
		// the facts table already holds rows (legacy DB, or facts written on a
		// build without FTS5), the inverted index would be empty/stale and the
		// sync triggers only fire on future writes - so we must rebuild it from
		// the content table. A COUNT(*) on an external-content FTS table reflects
		// the content table, not the index, so it can't be used to detect this.
		ftsExisted := s.tableExists(ctx, "facts_fts")
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
		// Backfill: if facts_fts was created fresh this Open but the facts table
		// already had rows, the inverted index is empty (triggers only fire on
		// future writes). Rebuild it from the content table so legacy rows are
		// searchable. On a DB where facts_fts already existed this is skipped.
		if s.ftsEnabled && !ftsExisted {
			var factCount int
			_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM facts`).Scan(&factCount)
			if factCount > 0 {
				if _, err := s.db.ExecContext(ctx, `INSERT INTO facts_fts(facts_fts) VALUES('rebuild')`); err != nil {
					// A failed rebuild shouldn't break Open; SearchFacts still
					// has the LIKE fallback for misses.
					s.ftsEnabled = false
				}
			}
		}
	}
	return nil
}

// addColumnIfMissing runs `ALTER TABLE <table> ADD COLUMN <ddl>` only when the
// column is not already present, so the additive migration is idempotent across
// boots and safe on a DB created before the column existed. SQLite has no
// `ADD COLUMN IF NOT EXISTS`, so presence is checked via PRAGMA table_info.
func (s *Store) addColumnIfMissing(ctx context.Context, table, column, ddl string) error {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notnull    int
			dfltValue  sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return rows.Close()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_ = rows.Close()
	_, err = s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, ddl))
	return err
}

// tableExists reports whether a table (or virtual table) with the given name is
// registered in sqlite_master.
func (s *Store) tableExists(ctx context.Context, name string) bool {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return err == nil && n > 0
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

// ClearSession wipes all chat data for a session but keeps the same session id
// and active flag. The room stays in the history list with an empty transcript.
func (s *Store) ClearSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("session id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM chat_turns WHERE session_id=?`,
		`DELETE FROM compaction_runs WHERE session_id=?`,
		`DELETE FROM context_snapshots WHERE session_id=?`,
		`DELETE FROM chat_events WHERE session_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, sessionID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chat_sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSession removes a session and all persisted chat data for it.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("session id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM chat_turns WHERE session_id=?`,
		`DELETE FROM compaction_runs WHERE session_id=?`,
		`DELETE FROM context_snapshots WHERE session_id=?`,
		`DELETE FROM chat_events WHERE session_id=?`,
		`DELETE FROM chat_sessions WHERE id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, sessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SessionSummary is a compact description of a stored chat session for the
// widget's history switcher. Title is derived from the first user turn so the
// switcher can show something more meaningful than the opaque session id.
type SessionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Active    bool   `json:"active"`
	TurnCount int    `json:"turn_count"`
	UpdatedAt string `json:"updated_at"`
	CreatedAt string `json:"created_at"`
}

// ListSessions returns recent sessions ordered by most-recently-updated first.
// A limit <= 0 falls back to a sensible default so callers can pass 0 for "all
// reasonable recent sessions". Each summary carries a derived title (first user
// turn snippet) and the count of turns that surface in the chat view.
func (s *Store) ListSessions(ctx context.Context, limit int) ([]SessionSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, active, created_at, updated_at
		FROM chat_sessions
		ORDER BY active DESC, updated_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionSummary
	for rows.Next() {
		var (
			summary SessionSummary
			active  int
		)
		if err := rows.Scan(&summary.ID, &active, &summary.CreatedAt, &summary.UpdatedAt); err != nil {
			return nil, err
		}
		summary.Active = active == 1
		out = append(out, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Enrich each session with a human title (first user turn) and a turn
	// count. Done in a second pass to keep the listing query simple and
	// portable across SQLite builds (no window functions needed).
	for i := range out {
		title, count, derr := s.sessionTitleAndCount(ctx, out[i].ID)
		if derr != nil {
			return nil, derr
		}
		out[i].Title = title
		out[i].TurnCount = count
	}
	return out, nil
}

// sessionTitleAndCount derives a display title from the first user turn and
// counts the user/assistant/error turns that surface in the chat view.
func (s *Store) sessionTitleAndCount(ctx context.Context, sessionID string) (string, int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chat_turns WHERE session_id=? AND role IN ('user','assistant','error')`,
		sessionID).Scan(&count); err != nil {
		return "", 0, err
	}
	var first sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT content FROM chat_turns WHERE session_id=? AND role='user' ORDER BY seq ASC LIMIT 1`,
		sessionID).Scan(&first); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	title := summarizeTitle(first.String)
	return title, count, nil
}

// summarizeTitle trims a turn body into a single-line switcher label. Empty
// content (e.g. an attachment-only turn or a brand-new session) yields an empty
// string so callers can fall back to a placeholder.
func summarizeTitle(content string) string {
	line := strings.TrimSpace(content)
	if line == "" {
		return ""
	}
	if idx := strings.IndexAny(line, "\r\n"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	const maxLen = 48
	if len([]rune(line)) > maxLen {
		runes := []rune(line)
		line = strings.TrimSpace(string(runes[:maxLen])) + "…"
	}
	return line
}

// Activate marks an existing session as the single active session. It mirrors
// the active-flag invariant used by Reset: every other session is deactivated
// in the same transaction. An unknown session id is rejected so the caller can
// surface a clear error instead of silently leaving no active session.
func (s *Store) Activate(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("session id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_sessions WHERE id=?`, sessionID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("session %q not found", sessionID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE chat_sessions SET active=0, updated_at=? WHERE active=1 AND id<>?`, now, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chat_sessions SET active=1, updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AppendTurn(ctx context.Context, sessionID, role, content string, tokenEstimate int) error {
	_, err := s.AppendTurnID(ctx, sessionID, role, content, tokenEstimate)
	return err
}

func (s *Store) AppendTurnID(ctx context.Context, sessionID, role, content string, tokenEstimate int) (int64, error) {
	return s.AppendTurnIDWithGeneration(ctx, sessionID, role, content, tokenEstimate, "")
}

func (s *Store) AppendTurnIDWithGeneration(ctx context.Context, sessionID, role, content string, tokenEstimate int, generationID string) (int64, error) {
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
	res, err := tx.ExecContext(ctx, `INSERT INTO chat_turns(session_id, seq, role, content, token_estimate, included_in_context, generation_id, created_at) VALUES (?, ?, ?, ?, ?, 1, ?, ?)`, sessionID, seq, role, content, tokenEstimate, nullString(generationID), now)
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
	var ckptIdx sql.NullInt64
	var created string
	var genID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, session_id, seq, role, content, token_estimate, included_in_context, compacted_at, checkpoint_index, generation_id, created_at
		FROM chat_turns WHERE session_id=? AND id=?`, sessionID, turnID).
		Scan(&t.ID, &t.SessionID, &t.Seq, &t.Role, &t.Content, &t.TokenEstimate, &included, &compacted, &ckptIdx, &genID, &created)
	if err != nil {
		return Turn{}, err
	}
	if ckptIdx.Valid {
		t.CheckpointIndex = int(ckptIdx.Int64)
	}
	t.IncludedInContext = included == 1
	if compacted.Valid && compacted.String != "" {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, compacted.String); parseErr == nil {
			t.CompactedAt = &parsed
		}
	}
	if genID.Valid {
		t.GenerationID = genID.String
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
	query := `SELECT id, session_id, seq, role, content, token_estimate, included_in_context, compacted_at, checkpoint_index, generation_id, created_at FROM chat_turns WHERE session_id=?`
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
		var ckptIdx sql.NullInt64
		var created string
		var genID sql.NullString
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Seq, &t.Role, &t.Content, &t.TokenEstimate, &included, &compacted, &ckptIdx, &genID, &created); err != nil {
			return nil, err
		}
		if genID.Valid {
			t.GenerationID = genID.String
		}
		t.IncludedInContext = included == 1
		if ckptIdx.Valid {
			t.CheckpointIndex = int(ckptIdx.Int64)
		}
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

// Checkpoint is the read view of one compaction checkpoint for a session.
type Checkpoint struct {
	Index          int    `json:"index"`
	SummaryTurnID  int64  `json:"summary_turn_id"`
	Summary        string `json:"summary"`
	Reason         string `json:"reason"`
	CompactedTurns int    `json:"compacted_turns"`
	TailStartTurnID int64 `json:"tail_start_turn_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// CheckpointResult is returned by CreateCheckpoint so the orchestrator can
// rebuild its in-memory message slice (latest checkpoint summary + tail) and
// emit a live UI event with the new index.
type CheckpointResult struct {
	Index          int    `json:"index"`
	SummaryTurnID  int64  `json:"summary_turn_id"`
	Reason         string `json:"reason"`
	CompactedTurns int    `json:"compacted_turns"`
	TailStartTurnID int64 `json:"tail_start_turn_id,omitempty"`
}

// TailPolicy describes which turns survive a compaction as the post-checkpoint
// tail. The orchestrator computes it via computeTailPreserve (anchored on the
// last assistant turn) and hands it to CreateCheckpoint so the store stays a
// dumb persistence layer.
type TailPolicy struct {
	// ArchiveTurnIDs are the turn ids to mark included_in_context=0 (archived
	// for UI, dropped from the model context). Everything NOT in this list and
	// not the new checkpoint turn stays in context.
	ArchiveTurnIDs []int64
	// TailStartTurnID is the first turn of the preserved tail (the anchored
	// last assistant turn or the turn before it). Stored on compaction_runs
	// for audit/UI.
	TailStartTurnID int64
}

// CreateCheckpoint persists one LLM-authored compaction checkpoint: it archives
// the supplied turn ids (rows remain for the UI), inserts a checkpoint marker
// turn (role=checkpoint) carrying the next monotonic per-session index, and
// records a compaction_runs row with the reason + tail anchor. It does NOT
// decide what to archive - the caller's TailPolicy does, so the anchoring rule
// (always keep the last assistant turn) lives in the orchestrator. Returns the
// new checkpoint index and summary turn id.
func (s *Store) CreateCheckpoint(ctx context.Context, sessionID, summary, reason string, tail TailPolicy, estimate func(string) int) (CheckpointResult, error) {
	if sessionID == "" {
		sessionID = defaultSessionID
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CheckpointResult{}, err
	}
	defer tx.Rollback()
	// Next monotonic checkpoint index for this session.
	var nextIndex int
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(checkpoint_index), 0) + 1 FROM compaction_runs WHERE session_id=?`, sessionID).Scan(&nextIndex)
	// Archive the supplied turns (drop from model context, keep for UI).
	archived := 0
	for _, id := range tail.ArchiveTurnIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE chat_turns SET included_in_context=0, compacted_at=? WHERE id=? AND session_id=?`, now, id, sessionID); err != nil {
			return CheckpointResult{}, err
		}
		archived++
	}
	// Insert the checkpoint marker turn itself. It is included_in_context=1 so
	// the context assembler replays it as the latest summary; the UI renders it
	// as a collapsible "Checkpoint n" card.
	var seq int
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM chat_turns WHERE session_id=?`, sessionID).Scan(&seq)
	res, err := tx.ExecContext(ctx, `INSERT INTO chat_turns(session_id, seq, role, content, token_estimate, included_in_context, checkpoint_index, created_at) VALUES (?, ?, 'checkpoint', ?, ?, 1, ?, ?)`, sessionID, seq, summary, estimate(summary), nextIndex, now)
	if err != nil {
		return CheckpointResult{}, err
	}
	summaryID, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO compaction_runs(session_id, summary_turn_id, compacted_turns, checkpoint_index, reason, tail_start_turn_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, sessionID, summaryID, archived, nextIndex, reason, nullInt64(tail.TailStartTurnID), now); err != nil {
		return CheckpointResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chat_sessions SET updated_at=? WHERE id=?`, now, sessionID); err != nil {
		return CheckpointResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CheckpointResult{}, err
	}
	return CheckpointResult{Index: nextIndex, SummaryTurnID: summaryID, Reason: reason, CompactedTurns: archived, TailStartTurnID: tail.TailStartTurnID}, nil
}

// LatestCheckpoint returns the most recent checkpoint for a session (highest
// checkpoint_index), with its summary text. Returns sql.ErrNoRows (wrapped) if
// the session has no checkpoint yet.
func (s *Store) LatestCheckpoint(ctx context.Context, sessionID string) (Checkpoint, error) {
	var ck Checkpoint
	var summaryID sql.NullInt64
	var tailStart sql.NullInt64
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT r.checkpoint_index, r.summary_turn_id, t.content, r.reason, r.compacted_turns, r.tail_start_turn_id, r.created_at
		FROM compaction_runs r LEFT JOIN chat_turns t ON t.id = r.summary_turn_id
		WHERE r.session_id=? ORDER BY r.checkpoint_index DESC LIMIT 1`, sessionID).
		Scan(&ck.Index, &summaryID, &ck.Summary, &ck.Reason, &ck.CompactedTurns, &tailStart, &created)
	if err != nil {
		return Checkpoint{}, err
	}
	if summaryID.Valid {
		ck.SummaryTurnID = summaryID.Int64
	}
	if tailStart.Valid {
		ck.TailStartTurnID = tailStart.Int64
	}
	if parsed, err := time.Parse(time.RFC3339Nano, created); err == nil {
		ck.CreatedAt = parsed
	}
	return ck, nil
}

// Checkpoints returns all checkpoints for a session, oldest first, for UI
// divider rendering. Only metadata is returned (no summary body) to keep the
// history payload small; the summary is loaded on expand from the turn itself.
func (s *Store) Checkpoints(ctx context.Context, sessionID string) ([]Checkpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.checkpoint_index, r.summary_turn_id, '', r.reason, r.compacted_turns, r.tail_start_turn_id, r.created_at
		FROM compaction_runs r WHERE r.session_id=? ORDER BY r.checkpoint_index ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Checkpoint
	for rows.Next() {
		var ck Checkpoint
		var summaryID sql.NullInt64
		var tailStart sql.NullInt64
		var created string
		if err := rows.Scan(&ck.Index, &summaryID, &ck.Summary, &ck.Reason, &ck.CompactedTurns, &tailStart, &created); err != nil {
			return nil, err
		}
		if summaryID.Valid {
			ck.SummaryTurnID = summaryID.Int64
		}
		if tailStart.Valid {
			ck.TailStartTurnID = tailStart.Int64
		}
		if parsed, err := time.Parse(time.RFC3339Nano, created); err == nil {
			ck.CreatedAt = parsed
		}
		out = append(out, ck)
	}
	return out, rows.Err()
}

// nullInt64 converts an int64 turn id to a sql.NullInt64 (0 -> NULL so the
// audit column is nullable).
func nullInt64(v int64) sql.NullInt64 {
	if v <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

func (s *Store) Usage(ctx context.Context, sessionID, provider, model string, contextWindow int) (Usage, error) {
	turns, err := s.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		// Return a Usage that still carries the resolved context window + ids so
		// callers (and the UI pill) don't degrade to "0/0" on a transient scan
		// error. The used-token count is unknown, but the window is not.
		return Usage{SessionID: sessionID, ContextWindow: contextWindow, Provider: provider, Model: model}, err
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

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
