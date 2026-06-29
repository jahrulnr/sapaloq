package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/hostcontext"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestClassifyIntent(t *testing.T) {
	cases := []struct {
		msg      string
		wantName string
		wantMode string
		minConf  float64
		maxConf  float64
	}{
		{"catat: beli susu besok", "catat", "personal", 0.75, 0.95},
		{"tolong catat note ini buat kerja klien", "catat", "work", 0.85, 0.95},
		{"notify me nanti sore", "notify", "personal", 0.75, 0.85},
		{"halo apa kabar", "chat", "personal", 0.2, 0.35},
		{"", "chat", "personal", 0.15, 0.25},
		{"jurnal hobby main game hari ini", "catat", "hobby", 0.75, 0.95},
	}
	for _, c := range cases {
		got := classifyIntent(c.msg)
		if got.Name != c.wantName {
			t.Errorf("classify(%q).Name = %q, want %q", c.msg, got.Name, c.wantName)
		}
		if got.Mode != c.wantMode {
			t.Errorf("classify(%q).Mode = %q, want %q", c.msg, got.Mode, c.wantMode)
		}
		if got.Confidence < c.minConf || got.Confidence > c.maxConf {
			t.Errorf("classify(%q).Confidence = %v, want in [%v,%v]", c.msg, got.Confidence, c.minConf, c.maxConf)
		}
	}
}

func newPrefetchOrch(t *testing.T) *Orchestrator {
	return newPrefetchFixture(t).orch
}

type prefetchFixture struct {
	orch *Orchestrator
	root string
}

func newPrefetchFixture(t *testing.T) prefetchFixture {
	t.Helper()
	root := t.TempDir()
	store, err := chatstore.Open(root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return prefetchFixture{
		orch: &Orchestrator{chat: store, cfg: config.DefaultConfig()},
		root: root,
	}
}

func (f prefetchFixture) prefetchLogLines(t *testing.T) int {
	t.Helper()
	path := filepath.Join(f.root, "memory", "prefetch_log.jsonl")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("open prefetch log: %v", err)
	}
	defer file.Close()
	n := 0
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n
}

func TestPrefetchContextLoadsNamespaceFacts(t *testing.T) {
	o := newPrefetchOrch(t)
	ctx := context.Background()
	// A high-confidence intent ("catat" twice via note) in personal namespace.
	if _, err := o.chat.UpsertFact(ctx, "personal", "preference", "notes_target", "personal-notes", "default notes go to personal-notes", 1); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	pkt := o.prefetchContext(ctx, "catat note: beli susu", hostcontext.SearchHints{})
	if pkt.Intent != "catat" {
		t.Fatalf("expected intent catat, got %q", pkt.Intent)
	}
	if pkt.Namespace != "personal" {
		t.Fatalf("expected personal namespace, got %q", pkt.Namespace)
	}
	if len(pkt.Facts) == 0 {
		t.Fatalf("expected namespace facts to be prefetched")
	}
	if !pkt.AntiDeepCheck {
		t.Fatalf("expected anti-deep-check at high confidence (%v)", pkt.Confidence)
	}
	block := pkt.render()
	if !strings.Contains(block, "personal-notes") {
		t.Fatalf("expected rendered block to contain the fact value, got:\n%s", block)
	}
	if !strings.Contains(block, "do not search the filesystem") {
		t.Fatalf("expected anti-deep-check directive in block, got:\n%s", block)
	}
}

func TestPrefetchLowConfidenceNoDirective(t *testing.T) {
	o := newPrefetchOrch(t)
	ctx := context.Background()
	pkt := o.prefetchContext(ctx, "halo apa kabar", hostcontext.SearchHints{})
	if pkt.AntiDeepCheck {
		t.Fatalf("low-confidence chat should not set anti-deep-check (conf=%v)", pkt.Confidence)
	}
	// No facts + low confidence → empty block (don't waste tokens).
	if b := pkt.render(); b != "" {
		t.Fatalf("expected empty block for low-confidence no-fact packet, got:\n%s", b)
	}
}

func TestPrefetchHotCacheServesRepeat(t *testing.T) {
	o := newPrefetchOrch(t)
	ctx := context.Background()
	_, _ = o.chat.UpsertFact(ctx, "personal", "routine", "morning", "stretch", "morning routine: stretch", 1)
	msg := "catat note morning routine"
	first := o.prefetchContext(ctx, msg, hostcontext.SearchHints{})
	if first.FromHotCache {
		t.Fatalf("first call should not be from hot cache")
	}
	second := o.prefetchContext(ctx, msg, hostcontext.SearchHints{})
	if !second.FromHotCache {
		t.Fatalf("identical repeat should be served from hot cache")
	}
}

func TestPrefetchRuleNarrowsKinds(t *testing.T) {
	o := newPrefetchOrch(t)
	ctx := context.Background()
	// Two kinds in the namespace; the rule restricts prefetch to "preference".
	_, _ = o.chat.UpsertFact(ctx, "personal", "preference", "k1", "v1", "pref one", 1)
	_, _ = o.chat.UpsertFact(ctx, "personal", "contact", "k2", "v2", "contact two", 1)
	if err := o.chat.UpsertPrefetchRule(ctx, chatstore.PrefetchRule{
		IntentPattern: "catat",
		Namespace:     "personal",
		FactKinds:     `["preference"]`,
	}); err != nil {
		t.Fatalf("rule: %v", err)
	}
	pkt := o.prefetchContext(ctx, "catat note something", hostcontext.SearchHints{})
	for _, f := range pkt.Facts {
		if f.Kind != "preference" {
			t.Fatalf("rule should restrict to preference kind, got %q", f.Kind)
		}
	}
	if len(pkt.Facts) == 0 {
		t.Fatalf("expected at least the preference fact")
	}
}

func TestPrefetchBlockDisabledByConfig(t *testing.T) {
	o := newPrefetchOrch(t)
	ctx := context.Background()
	_, _ = o.chat.UpsertFact(ctx, "personal", "preference", "k", "v", "some fact", 1)
	// Explicitly disable prefetch.
	o.cfg = config.Config{Memory: config.MemoryConfig{PrefetchEnabled: false}}
	// internalSet is unexported; WithDefaults would re-enable an unset block, so
	// force-disable via the threshold path: a block with a non-default threshold
	// but PrefetchEnabled=false stays disabled.
	o.cfg.Memory.PrefetchConfidenceThreshold = 0.7
	if b := o.prefetchBlock(ctx, "s", "catat note something"); b != "" {
		t.Fatalf("expected empty block when prefetch disabled, got:\n%s", b)
	}
}

func TestPrefetchSearchQueryIncludesHostPaths(t *testing.T) {
	q := hostcontext.PrefetchSearchQuery("hello", hostcontext.SearchHints{
		AttachmentPaths: []string{"/projects/foo/main.go"},
	})
	if !strings.Contains(q, "/projects/foo/main.go") {
		t.Fatalf("expected host path in search query, got %q", q)
	}
}

func TestPrefetchSearchQueryIncludesSessionWorkspace(t *testing.T) {
	q := hostcontext.PrefetchSearchQuery("hello", hostcontext.SearchHints{
		SessionWorkspace: "/projects/unique-workspace-segment",
	})
	if !strings.Contains(q, "/projects/unique-workspace-segment") {
		t.Fatalf("expected workspace in search query, got %q", q)
	}
}

func TestPrefetchCacheKeyDiffersByHostPaths(t *testing.T) {
	k1 := prefetchCacheKey("same message", hostcontext.SearchHints{AttachmentPaths: []string{"/a/x.go"}})
	k2 := prefetchCacheKey("same message", hostcontext.SearchHints{AttachmentPaths: []string{"/b/y.go"}})
	if k1 == k2 {
		t.Fatalf("cache keys must differ when host paths differ: %q", k1)
	}
}

func TestPrefetchHotCacheKeyDiffersByWorkspace(t *testing.T) {
	f := newPrefetchFixture(t)
	ctx := context.Background()
	msg := "catat note morning routine"
	paths := []string{"/workspace/a.go"}
	_, _ = f.orch.chat.UpsertFact(ctx, "personal", "routine", "morning", "stretch", "morning routine: stretch", 1)
	first := f.orch.prefetchContext(ctx, msg, hostcontext.SearchHints{
		SessionWorkspace: "/proj/a",
		AttachmentPaths:  paths,
	})
	if first.FromHotCache {
		t.Fatal("first call should not be from hot cache")
	}
	second := f.orch.prefetchContext(ctx, msg, hostcontext.SearchHints{
		SessionWorkspace: "/proj/b",
		AttachmentPaths:  paths,
	})
	if second.FromHotCache {
		t.Fatal("different host workspace must not share hot-cache entry")
	}
}

func TestPrefetchFactsHitByWorkspaceKeyword(t *testing.T) {
	o := newPrefetchOrch(t)
	ctx := context.Background()
	workspace := "/home/me/unique-project-segment"
	if _, err := o.chat.UpsertFact(ctx, "personal", "project", "root", workspace, "project notes for unique-project-segment", 1); err != nil {
		t.Fatal(err)
	}
	pkt := o.prefetchContext(ctx, "halo", hostcontext.SearchHints{SessionWorkspace: workspace})
	found := false
	for _, f := range pkt.Facts {
		if strings.Contains(f.Value, "unique-project-segment") || strings.Contains(f.Content, "unique-project-segment") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected workspace keyword to surface fact, got facts=%+v", pkt.Facts)
	}
}

func TestPrefetchHotCacheKeyDiffersByHostPaths(t *testing.T) {
	f := newPrefetchFixture(t)
	ctx := context.Background()
	msg := "catat note morning routine"
	_, _ = f.orch.chat.UpsertFact(ctx, "personal", "routine", "morning", "stretch", "morning routine: stretch", 1)
	first := f.orch.prefetchContext(ctx, msg, hostcontext.SearchHints{AttachmentPaths: []string{"/workspace/a.go"}})
	if first.FromHotCache {
		t.Fatal("first call should not be from hot cache")
	}
	second := f.orch.prefetchContext(ctx, msg, hostcontext.SearchHints{AttachmentPaths: []string{"/workspace/b.go"}})
	if second.FromHotCache {
		t.Fatal("different host paths must not share hot-cache entry")
	}
}

func TestPrefetchHotCacheKeyLongMessageKeepsPaths(t *testing.T) {
	f := newPrefetchFixture(t)
	ctx := context.Background()
	longMsg := strings.Repeat("catat note ", 20)
	_, _ = f.orch.chat.UpsertFact(ctx, "personal", "routine", "morning", "stretch", "morning routine: stretch", 1)
	_ = f.orch.prefetchContext(ctx, longMsg, hostcontext.SearchHints{AttachmentPaths: []string{"/workspace/unique-path-segment/file.go"}})
	second := f.orch.prefetchContext(ctx, longMsg, hostcontext.SearchHints{})
	if second.FromHotCache {
		t.Fatal("long message cache key must include host paths separately from truncated message digest")
	}
}

func TestPrefetchBlockLogsHostTelemetry(t *testing.T) {
	f := newPrefetchFixture(t)
	ctx := context.Background()
	sessionID := "chat-test"
	_, _ = f.orch.chat.UpsertFact(ctx, "personal", "preference", "k", "v", "some fact", 1)
	raw, _ := json.Marshal(hostcontext.Snapshot{
		Version: hostcontext.Version,
		Workspace: hostcontext.Workspace{
			SessionWorkspace: "/home/me/proj",
		},
		Attachments: []hostcontext.Attachment{
			{Path: "/home/me/proj/a.go", Kind: "file", Name: "a.go"},
		},
	})
	f.orch.setSessionHostContext(sessionID, raw)
	before := f.prefetchLogLines(t)
	_ = f.orch.prefetchBlock(ctx, sessionID, "catat note something")
	after := f.prefetchLogLines(t)
	if after != before+1 {
		t.Fatalf("prefetchBlock should append one log row: before=%d after=%d", before, after)
	}
	path := filepath.Join(f.root, "memory", "prefetch_log.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	last := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			last = line
		}
	}
	var rec struct {
		HostContextBytes int `json:"host_context_bytes"`
		AttachmentCount  int `json:"attachment_count"`
	}
	if err := json.Unmarshal([]byte(last), &rec); err != nil {
		t.Fatalf("decode log: %v line=%q", err, last)
	}
	if rec.HostContextBytes <= 0 {
		t.Fatalf("expected host_context_bytes > 0, got %d", rec.HostContextBytes)
	}
	if rec.AttachmentCount != 1 {
		t.Fatalf("expected attachment_count=1, got %d", rec.AttachmentCount)
	}
}

func TestEstimateOverheadDoesNotLogPrefetch(t *testing.T) {
	f := newPrefetchFixture(t)
	ctx := context.Background()
	sessionID := "chat-overhead"
	_, _ = f.orch.chat.UpsertFact(ctx, "personal", "preference", "k", "v", "some fact", 1)
	before := f.prefetchLogLines(t)
	_ = f.orch.estimatePerTurnOverhead(ctx, sessionID, "catat note something")
	after := f.prefetchLogLines(t)
	if after != before {
		t.Fatalf("estimatePerTurnOverhead must not log prefetch telemetry: before=%d after=%d", before, after)
	}
}
