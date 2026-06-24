CREATE TABLE IF NOT EXISTS facts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  content TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Context-SOP memory columns (additive; see docs/CONTEXT-SOP.md). The original
-- facts schema only had (kind, content, created_at). These columns let a fact
-- be addressed as a typed, namespaced key/value with a confidence and a
-- soft-delete marker (obsolete_at) so the index can hold preferences, routines,
-- decisions, etc. A fresh DB gets them here; an existing DB is upgraded via the
-- idempotent ALTER TABLE pass in store.go migrate().
ALTER TABLE facts ADD COLUMN namespace TEXT NOT NULL DEFAULT 'default';
ALTER TABLE facts ADD COLUMN key TEXT;
ALTER TABLE facts ADD COLUMN value TEXT;
ALTER TABLE facts ADD COLUMN confidence REAL NOT NULL DEFAULT 1.0;
ALTER TABLE facts ADD COLUMN obsolete_at TEXT;
ALTER TABLE facts ADD COLUMN updated_at TEXT;

CREATE INDEX IF NOT EXISTS idx_facts_namespace_kind ON facts(namespace, kind, obsolete_at);
CREATE INDEX IF NOT EXISTS idx_facts_key ON facts(namespace, kind, key);

CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(content, content='facts', content_rowid='id');

CREATE TRIGGER IF NOT EXISTS facts_ai AFTER INSERT ON facts BEGIN
  INSERT INTO facts_fts(rowid, content) VALUES (new.id, new.content);
END;

CREATE TRIGGER IF NOT EXISTS facts_ad AFTER DELETE ON facts BEGIN
  INSERT INTO facts_fts(facts_fts, rowid, content) VALUES('delete', old.id, old.content);
END;

CREATE TRIGGER IF NOT EXISTS facts_au AFTER UPDATE ON facts BEGIN
  INSERT INTO facts_fts(facts_fts, rowid, content) VALUES('delete', old.id, old.content);
  INSERT INTO facts_fts(rowid, content) VALUES (new.id, new.content);
END;

CREATE TABLE IF NOT EXISTS chat_sessions (
  id TEXT PRIMARY KEY,
  namespace TEXT NOT NULL DEFAULT 'default',
  provider TEXT,
  model TEXT,
  active INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  reset_at TEXT
);

CREATE TABLE IF NOT EXISTS chat_turns (
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
);
CREATE INDEX IF NOT EXISTS idx_chat_turns_session_seq ON chat_turns(session_id, seq);

CREATE TABLE IF NOT EXISTS chat_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS context_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  used_tokens INTEGER NOT NULL,
  context_window INTEGER NOT NULL,
  percent INTEGER NOT NULL,
  provider TEXT,
  model TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS compaction_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  summary_turn_id INTEGER,
  compacted_turns INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

-- skills_index: SapaLOQ-local skills registry (id → triggers/path/max_tokens).
-- Populated at boot from skills/*.md so the assembler reads paths from the index
-- instead of walking the filesystem per turn (see docs/CONTEXT-SOP.md boot sync).
CREATE TABLE IF NOT EXISTS skills_index (
  id TEXT PRIMARY KEY,
  triggers TEXT NOT NULL DEFAULT '[]',
  path TEXT NOT NULL DEFAULT '',
  max_tokens INTEGER NOT NULL DEFAULT 0,
  priority INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);

-- prefetch_rules: intent → prefetch mapping plus bandit-style telemetry. The
-- ingress pipeline looks an intent up here to know which fact kinds / skills /
-- config keys to prefetch, and the success_rate/hit_count feed rule tuning.
CREATE TABLE IF NOT EXISTS prefetch_rules (
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
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prefetch_rules_intent ON prefetch_rules(intent_pattern, namespace);

-- prompt_slices: dynamic system-prompt templates, indexed from
-- prompt/slices/*.md + modes/*.md at boot. The assembler reads template_path
-- and conditions from here (see docs/PROMPT-BUILDER-SOP.md).
CREATE TABLE IF NOT EXISTS prompt_slices (
  id TEXT PRIMARY KEY,
  role TEXT NOT NULL DEFAULT '',
  conditions TEXT NOT NULL DEFAULT '{}',
  template_path TEXT NOT NULL,
  token_budget INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);

-- learning_queue: async learning events drained by the memory-janitor. Rows
-- with processed_at IS NULL are pending. payload is opaque JSON.
CREATE TABLE IF NOT EXISTS learning_queue (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_kind TEXT NOT NULL,
  payload TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  processed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_learning_queue_unprocessed ON learning_queue(processed_at, id);

-- hot_cache: optional restart warm-up / repeat-within-5-min serve. Bounded by
-- expires_at; expired rows are pruned lazily on read.
CREATE TABLE IF NOT EXISTS hot_cache (
  cache_key TEXT PRIMARY KEY,
  payload TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);

-- prefetch_log: telemetry for prefetch rule tuning. One row per ingress.
CREATE TABLE IF NOT EXISTS prefetch_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL DEFAULT '',
  intent TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0,
  deep_check_used INTEGER NOT NULL DEFAULT 0,
  task_success INTEGER,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS feedback_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  turn_id INTEGER,
  signal TEXT NOT NULL,
  reward REAL NOT NULL,
  correction TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
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
);
CREATE INDEX IF NOT EXISTS idx_nodes_role ON nodes(role, enabled, priority DESC);
