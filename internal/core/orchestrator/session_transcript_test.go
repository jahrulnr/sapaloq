package orchestrator

import (
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestMergeTranscriptItemsThinkingBeforeAssistant(t *testing.T) {
	at := time.Now().UTC()
	turns := []chatstore.Turn{
		{ID: 1, Seq: 1, Role: "user", Content: "heyy", GenerationID: "1", CreatedAt: at},
		{ID: 2, Seq: 2, Role: "assistant", Content: "Hey!", GenerationID: "1", CreatedAt: at.Add(2 * time.Millisecond)},
		{ID: 3, Seq: 3, Role: "thinking", Content: "reasoning", GenerationID: "1", CreatedAt: at.Add(time.Millisecond)},
	}
	out := mergeTranscriptItems(turns, nil)
	if len(out) != 3 {
		t.Fatalf("entries = %d, want 3", len(out))
	}
	if out[1].Kind != bridge.TranscriptThinking || out[2].Kind != bridge.TranscriptText {
		t.Fatalf("order = %q then %q, want thinking before text", out[1].Kind, out[2].Kind)
	}
}

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
