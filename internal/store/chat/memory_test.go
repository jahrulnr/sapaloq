package chat

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestMigrationUpgradesLegacyFacts proves that a companion.db created with the
// pre-Context-SOP facts schema (kind, content, created_at) is upgraded
// idempotently: the new columns appear, the legacy row survives with a default
// namespace, and a second Open is a no-op (no duplicate-column error).
func TestMigrationUpgradesLegacyFacts(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "companion.db")

	// Seed a legacy DB by hand: the original bare facts schema + one row.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE facts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		kind TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("legacy create: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO facts(kind, content, created_at) VALUES ('preference','legacy likes dark mode','2026-01-01 10:00:00')`); err != nil {
		t.Fatalf("legacy insert: %v", err)
	}
	_ = raw.Close()

	// Open via the store: migrate() must add the new columns without error.
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open (migrate): %v", err)
	}
	ctx := context.Background()

	cols := factColumns(t, s)
	for _, want := range []string{"namespace", "key", "value", "confidence", "obsolete_at", "updated_at"} {
		if !cols[want] {
			t.Fatalf("expected facts column %q after migration; have %v", want, cols)
		}
	}

	// Legacy row survives and is searchable, with namespace defaulted.
	got, err := s.SearchFacts(ctx, "dark", nil, 10)
	if err != nil {
		t.Fatalf("search legacy: %v", err)
	}
	if len(got) != 1 || got[0].Content != "legacy likes dark mode" {
		t.Fatalf("legacy row lost after migration: %+v", got)
	}
	if got[0].Namespace != "default" {
		t.Fatalf("expected default namespace, got %q", got[0].Namespace)
	}
	_ = s.Close()

	// Reopen: idempotent — addColumnIfMissing must not re-ALTER.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen (idempotent migrate): %v", err)
	}
	defer s2.Close()
	again, err := s2.SearchFacts(ctx, "dark", nil, 10)
	if err != nil {
		t.Fatalf("search after reopen: %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("expected 1 row after reopen, got %d", len(again))
	}
}

func TestUpsertFactDedupeByKey(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	id1, err := s.UpsertFact(ctx, "personal", "preference", "notes_target", "personal-notes", "", 0.9)
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	// Same namespace+kind+key → in-place update, same id, new value.
	id2, err := s.UpsertFact(ctx, "personal", "preference", "notes_target", "work-notes", "", 0.5)
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("expected upsert to reuse id %d, got %d", id1, id2)
	}

	facts, err := s.FactsByNamespace(ctx, "personal", "preference", 10)
	if err != nil {
		t.Fatalf("by namespace: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 deduped fact, got %d (%+v)", len(facts), facts)
	}
	if facts[0].Value != "work-notes" {
		t.Fatalf("expected updated value 'work-notes', got %q", facts[0].Value)
	}

	// A different namespace is a distinct fact.
	if _, err := s.UpsertFact(ctx, "work", "preference", "notes_target", "work-notes", "", 1.0); err != nil {
		t.Fatalf("upsert other ns: %v", err)
	}
	work, err := s.FactsByNamespace(ctx, "work", "", 10)
	if err != nil {
		t.Fatalf("work ns: %v", err)
	}
	if len(work) != 1 {
		t.Fatalf("expected 1 fact in work ns, got %d", len(work))
	}
}

func TestUpsertFactEmptyInputs(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	// Blank kind → no-op.
	if id, err := s.UpsertFact(ctx, "personal", "  ", "k", "v", "", 1); err != nil || id != 0 {
		t.Fatalf("blank kind should be no-op, got id=%d err=%v", id, err)
	}
	// Empty key + content derives content from key/value; here all empty → no-op.
	if id, err := s.UpsertFact(ctx, "personal", "note", "", "", "", 1); err != nil || id != 0 {
		t.Fatalf("empty content should be no-op, got id=%d err=%v", id, err)
	}
	// content derived from key+value when content omitted.
	id, err := s.UpsertFact(ctx, "personal", "note", "remind", "buy milk", "", 1)
	if err != nil || id == 0 {
		t.Fatalf("derived-content upsert failed: id=%d err=%v", id, err)
	}
	got, err := s.SearchFacts(ctx, "milk", nil, 10)
	if err != nil {
		t.Fatalf("search derived: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected derived-content fact searchable, got %+v", got)
	}
}

func TestObsoleteFactHiddenFromSearch(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	id, err := s.UpsertFact(ctx, "personal", "decision", "stack", "use postgres", "use postgres for storage", 1)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got, _ := s.SearchFacts(ctx, "postgres", nil, 10); len(got) != 1 {
		t.Fatalf("expected 1 before obsolete, got %d", len(got))
	}
	if err := s.ObsoleteFact(ctx, id); err != nil {
		t.Fatalf("obsolete: %v", err)
	}
	if got, _ := s.SearchFacts(ctx, "postgres", nil, 10); len(got) != 0 {
		t.Fatalf("expected 0 after obsolete (FTS path), got %d", len(got))
	}
	if got, _ := s.FactsByNamespace(ctx, "personal", "", 10); len(got) != 0 {
		t.Fatalf("expected 0 from namespace after obsolete, got %d", len(got))
	}
	// LIKE fallback path must also hide it.
	s.ftsEnabled = false
	if got, _ := s.SearchFacts(ctx, "postgres", nil, 10); len(got) != 0 {
		t.Fatalf("expected 0 via LIKE after obsolete, got %d", len(got))
	}
}

func TestPrefetchRules(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.UpsertPrefetchRule(ctx, PrefetchRule{
		IntentPattern: "catat",
		Namespace:     "personal",
		FactKinds:     `["preference","routine"]`,
		SkillIDs:      `["sapaloq-scribe"]`,
	}); err != nil {
		t.Fatalf("upsert rule: %v", err)
	}

	r, ok, err := s.PrefetchRule(ctx, "catat", "personal")
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	if r.FactKinds != `["preference","routine"]` {
		t.Fatalf("unexpected fact_kinds %q", r.FactKinds)
	}

	// Namespace fallback to default.
	if err := s.UpsertPrefetchRule(ctx, PrefetchRule{IntentPattern: "notify", Namespace: "default", SkillIDs: `["x"]`}); err != nil {
		t.Fatalf("upsert default rule: %v", err)
	}
	if _, ok, _ := s.PrefetchRule(ctx, "notify", "personal"); !ok {
		t.Fatalf("expected default-namespace fallback to match")
	}

	// Telemetry: 2 hits, 1 success → success_rate 0.5.
	if err := s.RecordPrefetchHit(ctx, "catat", "personal", true); err != nil {
		t.Fatalf("hit 1: %v", err)
	}
	if err := s.RecordPrefetchHit(ctx, "catat", "personal", false); err != nil {
		t.Fatalf("hit 2: %v", err)
	}
	r, _, _ = s.PrefetchRule(ctx, "catat", "personal")
	if r.HitCount != 2 || r.SuccessCount != 1 {
		t.Fatalf("expected 2 hits / 1 success, got %d/%d", r.HitCount, r.SuccessCount)
	}
	if r.SuccessRate < 0.49 || r.SuccessRate > 0.51 {
		t.Fatalf("expected success_rate ~0.5, got %v", r.SuccessRate)
	}

	// Unknown intent → not ok, no error.
	if _, ok, err := s.PrefetchRule(ctx, "nope", "personal"); ok || err != nil {
		t.Fatalf("expected miss for unknown intent, ok=%v err=%v", ok, err)
	}
}

func TestHotCacheTTL(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.HotCacheSet(ctx, "k", "payload", 50*time.Millisecond); err != nil {
		t.Fatalf("set: %v", err)
	}
	if v, ok, _ := s.HotCacheGet(ctx, "k"); !ok || v != "payload" {
		t.Fatalf("expected hit, ok=%v v=%q", ok, v)
	}
	// Overwrite.
	if err := s.HotCacheSet(ctx, "k", "payload2", time.Minute); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if v, _, _ := s.HotCacheGet(ctx, "k"); v != "payload2" {
		t.Fatalf("expected overwrite, got %q", v)
	}

	// Expiry (lazy on read).
	if err := s.HotCacheSet(ctx, "exp", "old", time.Nanosecond); err != nil {
		t.Fatalf("set exp: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, ok, _ := s.HotCacheGet(ctx, "exp"); ok {
		t.Fatalf("expected expiry miss")
	}
	// Prune removes nothing pending and reports a count >= 0.
	if _, err := s.PruneHotCache(ctx); err != nil {
		t.Fatalf("prune: %v", err)
	}
}

func TestLearningQueueDrain(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	id1, err := s.EnqueueLearning(ctx, "promote", `{"key":"a"}`)
	if err != nil || id1 == 0 {
		t.Fatalf("enqueue 1: id=%d err=%v", id1, err)
	}
	_, _ = s.EnqueueLearning(ctx, "promote", `{"key":"b"}`)
	// Blank kind → no-op.
	if id, _ := s.EnqueueLearning(ctx, "  ", "{}"); id != 0 {
		t.Fatalf("blank kind should not enqueue")
	}

	pending, err := s.PendingLearning(ctx, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(pending))
	}
	if pending[0].ID != id1 {
		t.Fatalf("expected oldest first (id %d), got %d", id1, pending[0].ID)
	}

	if err := s.MarkLearningProcessed(ctx, id1); err != nil {
		t.Fatalf("mark: %v", err)
	}
	// Re-mark is idempotent.
	if err := s.MarkLearningProcessed(ctx, id1); err != nil {
		t.Fatalf("re-mark: %v", err)
	}
	pending, _ = s.PendingLearning(ctx, 10)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending after drain, got %d", len(pending))
	}
}

func TestSkillIndexAndPromptSlices(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.UpsertSkillIndex(ctx, SkillIndexEntry{ID: "scribe", Triggers: `["catat","note"]`, Path: "~/SapaLOQ/skills/scribe.md", MaxTokens: 800, Priority: 5}); err != nil {
		t.Fatalf("upsert skill: %v", err)
	}
	// Update keeps single row.
	if err := s.UpsertSkillIndex(ctx, SkillIndexEntry{ID: "scribe", Triggers: `["catat"]`, Path: "p", Priority: 9}); err != nil {
		t.Fatalf("update skill: %v", err)
	}
	skills, err := s.SkillIndexEntries(ctx)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(skills) != 1 || skills[0].Priority != 9 {
		t.Fatalf("expected 1 updated skill priority 9, got %+v", skills)
	}

	if err := s.UpsertPromptSlice(ctx, PromptSlice{ID: "mode-personal", Role: "orchestrator", TemplatePath: "modes/personal.md", TokenBudget: 200}); err != nil {
		t.Fatalf("upsert slice: %v", err)
	}
	if err := s.UpsertPromptSlice(ctx, PromptSlice{ID: "", TemplatePath: "x"}); err != nil {
		t.Fatalf("blank-id slice should be no-op, got err %v", err)
	}
	got, err := s.PromptSlicesForRole(ctx, "orchestrator")
	if err != nil {
		t.Fatalf("slices: %v", err)
	}
	if len(got) != 1 || got[0].TemplatePath != "modes/personal.md" {
		t.Fatalf("expected 1 slice, got %+v", got)
	}
}

func TestUpsertFactConcurrent(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.UpsertFact(ctx, "personal", "routine", "morning", "stretch", "", 1)
		}()
	}
	wg.Wait()
	// Concurrent upserts on the same key may race to insert before the first
	// commits, so allow a small number of rows but assert it didn't explode and
	// the namespace query still works.
	facts, err := s.FactsByNamespace(ctx, "personal", "routine", 100)
	if err != nil {
		t.Fatalf("by namespace: %v", err)
	}
	if len(facts) == 0 {
		t.Fatalf("expected at least one fact after concurrent upserts")
	}
}

// openStore opens a fresh store in a temp dir and registers cleanup.
func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// factColumns returns the set of column names on the facts table.
func factColumns(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.db.Query(`PRAGMA table_info(facts)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notnull    int
			dflt       sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &primaryKey); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cols[name] = true
	}
	return cols
}
