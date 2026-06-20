package chat

import (
	"context"
	"testing"
)

func TestNodesUpsertAndGet(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	n := Node{Name: "local-default", Role: "*", Wrapper: "local", Communicate: "unix", Enabled: true, ShareMemory: true, Capabilities: []string{"all"}}
	if err := s.UpsertNode(ctx, n); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := s.GetNode(ctx, "local-default")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Role != "*" || !got.IsLocal() || !got.ShareMemory || len(got.Capabilities) != 1 {
		t.Fatalf("unexpected node: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("created_at not set")
	}

	// Update preserves created_at, changes priority.
	n.Priority = 5
	if err := s.UpsertNode(ctx, n); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, _, _ := s.GetNode(ctx, "local-default")
	if got2.Priority != 5 {
		t.Fatalf("priority not updated: %+v", got2)
	}
	if !got2.CreatedAt.Equal(got.CreatedAt) {
		t.Fatalf("created_at should be preserved on update: %v vs %v", got2.CreatedAt, got.CreatedAt)
	}
}

func TestNodesForRoleOrdersAndFilters(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	_ = s.UpsertNode(ctx, Node{Name: "local-default", Role: "*", Wrapper: "local", Communicate: "unix", Enabled: true, Priority: 0})
	_ = s.UpsertNode(ctx, Node{Name: "remote-scribe", Role: "scribe", Wrapper: "remote", Communicate: "ws", Address: "wss://x", Enabled: true, Priority: 10})
	_ = s.UpsertNode(ctx, Node{Name: "disabled-one", Role: "scribe", Wrapper: "remote", Communicate: "ws", Enabled: false, Priority: 99})

	got, err := s.NodesForRole(ctx, "scribe")
	if err != nil {
		t.Fatalf("NodesForRole: %v", err)
	}
	// disabled excluded; remote-scribe (10) before local-default (0).
	if len(got) != 2 {
		t.Fatalf("want 2 enabled scribe-eligible nodes, got %d: %+v", len(got), got)
	}
	if got[0].Name != "remote-scribe" || got[1].Name != "local-default" {
		t.Fatalf("priority ordering wrong: %+v", got)
	}
}

func TestNodesSetEnabledAndTouch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	_ = s.UpsertNode(ctx, Node{Name: "n1", Role: "*", Wrapper: "local", Communicate: "unix", Enabled: true})
	if err := s.SetNodeEnabled(ctx, "n1", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, _, _ := s.GetNode(ctx, "n1")
	if got.Enabled {
		t.Fatalf("node should be disabled")
	}
	if err := s.TouchNode(ctx, "n1", "boom"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, _, _ = s.GetNode(ctx, "n1")
	if got.LastError != "boom" || got.LastSeenAt == "" {
		t.Fatalf("touch did not record: %+v", got)
	}
}
