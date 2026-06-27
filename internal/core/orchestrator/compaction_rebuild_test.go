package orchestrator

import (
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestRebuildMessagesFromCheckpointSkipsCheckpointTailRole(t *testing.T) {
	prefix := []bridge.Message{
		{Role: "system", Content: "persona"},
	}
	ckpt := chatstore.Checkpoint{Index: 1, Summary: "## Task\nDone."}
	tail := []chatstore.Turn{
		{Role: "assistant", Content: "last action"},
		{Role: "checkpoint", Content: "## Task\nDone."},
		{Role: "tool", Content: "tool output"},
	}
	got := rebuildMessagesFromCheckpoint(prefix, ckpt, tail)

	wantRoles := []string{"system", "system", "assistant", "tool"}
	if len(got) != len(wantRoles) {
		t.Fatalf("got %d messages, want %d: %+v", len(got), len(wantRoles), got)
	}
	for i, want := range wantRoles {
		if got[i].Role != want {
			t.Errorf("message %d role = %q, want %q", i, got[i].Role, want)
		}
	}
	if !strings.Contains(got[1].Content, "[Checkpoint 1 summary]") {
		t.Fatalf("expected formatted checkpoint summary, got %q", got[1].Content)
	}
}
