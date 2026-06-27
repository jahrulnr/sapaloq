package orchestrator

import (
	"context"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// newSessionOrchestrator builds an Orchestrator backed by a real chat store so
// the session-switcher methods (ListSessions/SwitchSession/NewSession) exercise
// the same persistence path as production.
func newSessionOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	store, err := chatstore.Open(dir)
	if err != nil {
		t.Fatalf("open chat store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &Orchestrator{
		memoryDir: dir,
		cfg:       config.Config{},
		entry:     config.LLMBridge{Key: "p", Model: "m"},
		chat:      store,
	}
}

func TestOrchestratorSessionSwitchFlow(t *testing.T) {
	o := newSessionOrchestrator(t)
	ctx := context.Background()

	// First session created lazily via ActiveSession.
	first, err := o.ActiveSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A brand-new session via NewSession becomes active and is distinct.
	second, err := o.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("NewSession must mint a distinct session id")
	}
	if active, _ := o.ActiveSession(ctx); active != second {
		t.Fatalf("expected new session %q active, got %q", second, active)
	}

	// Switch back to the first session.
	got, err := o.SwitchSession(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("SwitchSession returned %q, want %q", got, first)
	}
	if active, _ := o.ActiveSession(ctx); active != first {
		t.Fatalf("expected %q active after switch, got %q", first, active)
	}

	// ListSessions returns both, active sorted first.
	sessions, err := o.ListSessions(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if !sessions[0].Active || sessions[0].ID != first {
		t.Fatalf("expected active %q first, got %#v", first, sessions[0])
	}
}

func TestOrchestratorSwitchSessionUnknownIDFails(t *testing.T) {
	o := newSessionOrchestrator(t)
	ctx := context.Background()
	if _, err := o.ActiveSession(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := o.SwitchSession(ctx, "missing"); err == nil {
		t.Fatal("expected error switching to unknown session id")
	}
}
