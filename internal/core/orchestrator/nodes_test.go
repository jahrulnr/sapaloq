package orchestrator

import (
	"context"
	"testing"

	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func newNodesOrch(t *testing.T) *Orchestrator {
	t.Helper()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open chat store: %v", err)
	}
	return &Orchestrator{chat: store}
}

func TestBootstrapLocalDefaultIdempotent(t *testing.T) {
	o := newNodesOrch(t)
	ctx := context.Background()
	o.bootstrapLocalDefaultNode(ctx)
	o.bootstrapLocalDefaultNode(ctx) // second call must not duplicate or error

	nodes, err := o.chat.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	count := 0
	for _, n := range nodes {
		if n.Name == localDefaultNode {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want exactly one local-default, got %d", count)
	}
	n, ok, _ := o.chat.GetNode(ctx, localDefaultNode)
	if !ok || !n.IsLocal() || !n.Enabled {
		t.Fatalf("local-default should be enabled+local: %+v", n)
	}
}

func TestPickNodeFallsBackToLocalDefault(t *testing.T) {
	o := newNodesOrch(t)
	ctx := context.Background()
	o.bootstrapLocalDefaultNode(ctx)

	got := o.pickNode(ctx, "task-runner", "")
	if got.Name != localDefaultNode {
		t.Fatalf("expected local-default fallback, got %q", got.Name)
	}
}

func TestPickNodeHonorsHint(t *testing.T) {
	o := newNodesOrch(t)
	ctx := context.Background()
	o.bootstrapLocalDefaultNode(ctx)
	_ = o.chat.UpsertNode(ctx, chatstore.Node{Name: "special", Role: "*", Wrapper: "local", Communicate: "unix", Enabled: true, Priority: 1})

	got := o.pickNode(ctx, "task-runner", "special")
	if got.Name != "special" {
		t.Fatalf("hint should win, got %q", got.Name)
	}
	// disabled hint is ignored → falls through to role/priority/fallback
	_ = o.chat.SetNodeEnabled(ctx, "special", false)
	got = o.pickNode(ctx, "task-runner", "special")
	if got.Name == "special" {
		t.Fatalf("disabled hint should be ignored, got %q", got.Name)
	}
}

func TestPickNodePrefersHighestPriorityForRole(t *testing.T) {
	o := newNodesOrch(t)
	ctx := context.Background()
	o.bootstrapLocalDefaultNode(ctx) // priority 0, role *
	_ = o.chat.UpsertNode(ctx, chatstore.Node{Name: "scribe-hi", Role: "scribe", Wrapper: "local", Communicate: "unix", Enabled: true, Priority: 10})

	got := o.pickNode(ctx, "scribe", "")
	if got.Name != "scribe-hi" {
		t.Fatalf("expected highest-priority role node, got %q", got.Name)
	}
}

func TestPickNodeNilStoreSafe(t *testing.T) {
	o := &Orchestrator{}
	got := o.pickNode(context.Background(), "task-runner", "")
	if got.Name != localDefaultNode || !got.IsLocal() {
		t.Fatalf("nil-store pick should be synthetic local-default, got %+v", got)
	}
}
