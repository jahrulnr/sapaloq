package orchestrator

import (
	"context"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestRebuildMessagesFromCheckpointUsesReplayMapper(t *testing.T) {
	prefix := []bridge.Message{{Role: "system", Content: "persona"}}
	ckpt := chatstore.Checkpoint{Index: 1, Summary: "summary"}
	tail := []chatstore.Turn{
		{Role: "tool", Content: "[Tool results]\nout", GenerationID: "1"},
		{Role: "assistant", Content: "done\n\n[Called tools: exec]", GenerationID: "1"},
	}
	got := rebuildMessagesFromCheckpoint(prefix, ckpt, tail)
	if len(got) != 4 {
		t.Fatalf("messages = %d, want 4 (prefix+ckpt+assistant+tool)", len(got))
	}
	if got[2].Role != "assistant" || got[3].Role != "tool" {
		t.Fatalf("replay order = %s,%s want assistant,tool", got[2].Role, got[3].Role)
	}
}

func TestReplayContextRefreshUsesMapper(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.AppendTurn(ctx, sessionID, "user", "go", 1)
	_ = store.AppendTurn(ctx, sessionID, "tool", "[Tool results]\nok", 5)
	_ = store.AppendTurn(ctx, sessionID, "assistant", "done\n\n[Called tools: exec]", 3)

	o := &Orchestrator{chat: store}
	rc := newReplayContext(o, sessionID, []bridge.Message{
		{Role: "system", Content: "sys"},
	})
	msgs, err := rc.Messages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want sys+user+assistant+tool", len(msgs))
	}
	if msgs[2].Role != "assistant" || msgs[3].Role != "tool" {
		t.Fatalf("order = %s,%s want assistant,tool", msgs[2].Role, msgs[3].Role)
	}
}
