package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestClassifyIntent(t *testing.T) {
	cases := []struct {
		msg        string
		wantName   string
		wantMode   string
		minConf    float64
		maxConf    float64
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
	t.Helper()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &Orchestrator{chat: store}
}

func TestPrefetchContextLoadsNamespaceFacts(t *testing.T) {
	o := newPrefetchOrch(t)
	ctx := context.Background()
	// A high-confidence intent ("catat" twice via note) in personal namespace.
	if _, err := o.chat.UpsertFact(ctx, "personal", "preference", "notes_target", "personal-notes", "default notes go to personal-notes", 1); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	pkt := o.prefetchContext(ctx, "catat note: beli susu")
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
	pkt := o.prefetchContext(ctx, "halo apa kabar")
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
	first := o.prefetchContext(ctx, msg)
	if first.FromHotCache {
		t.Fatalf("first call should not be from hot cache")
	}
	second := o.prefetchContext(ctx, msg)
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
	pkt := o.prefetchContext(ctx, "catat note something")
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
