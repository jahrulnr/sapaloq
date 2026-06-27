package orchestrator

import (
	"testing"
	"time"

	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestTurnToEntryStripsCalledToolsNote(t *testing.T) {
	entries := turnToEntry(chatstore.Turn{
		ID:        3,
		Seq:       3,
		Role:      "assistant",
		Content:   "Delegasi ke agent.\n\n[Called tools: sapaloq_spawn_agent]",
		CreatedAt: time.Now().UTC(),
	})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Text != "Delegasi ke agent." {
		t.Fatalf("text = %q", entries[0].Text)
	}
	onlyNote := turnToEntry(chatstore.Turn{
		ID:      4,
		Role:    "assistant",
		Content: "[Called tools: sapaloq_stop]",
	})
	if len(onlyNote) != 0 {
		t.Fatalf("note-only turn should not surface in transcript, got %#v", onlyNote)
	}
}
