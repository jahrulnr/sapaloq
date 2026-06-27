-- migrations/002_checkpoints.sql
-- LLM-driven checkpoint compaction (see docs/CONTEXT-SOP.md).
--
-- The original compaction schema (001_initial.sql) stored a single heuristic
-- summary turn + a compaction_runs row. The checkpoint model adds:
--   - a monotonic per-session checkpoint_index,
--   - a reason (model | force_headroom | force_overflow | manual),
--   - the checkpoint marker turn on chat_turns (role=checkpoint) carrying that
--     index, so the UI can render a "Checkpoint n" divider and the context
--     assembler can replay only the latest checkpoint summary + anchored tail.
--
-- These are additive ALTERs; the chat store applies them idempotently at boot
-- via addColumnIfMissing. This file documents the canonical schema for a fresh
-- database.

-- chat_turns: 0/NULL = normal turn; N = checkpoint marker turn (role=checkpoint).
ALTER TABLE chat_turns ADD COLUMN checkpoint_index INTEGER;
CREATE INDEX IF NOT EXISTS idx_chat_turns_checkpoint ON chat_turns(session_id, checkpoint_index);

-- compaction_runs: which checkpoint this run created and why.
ALTER TABLE compaction_runs ADD COLUMN checkpoint_index INTEGER;
ALTER TABLE compaction_runs ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE compaction_runs ADD COLUMN tail_start_turn_id INTEGER;
CREATE INDEX IF NOT EXISTS idx_compaction_runs_session_idx ON compaction_runs(session_id, checkpoint_index);
