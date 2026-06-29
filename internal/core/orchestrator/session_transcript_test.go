package orchestrator

import (
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestMergeTranscriptItemsUsesChronologyForLegacyTurnSequence(t *testing.T) {
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

func TestMergeTranscriptItemsPreservesInferenceRoundOrder(t *testing.T) {
	at := time.Now().UTC()
	turns := []chatstore.Turn{
		{ID: 1, Seq: 1, Role: "user", Content: "hy hy", GenerationID: "3", CreatedAt: at},
		// Autopilot is hidden model input between inference rounds. Its persisted
		// timestamp may precede the first round turns because those are flushed
		// only after the provider response completes.
		{ID: 2, Seq: 2, Role: "autopilot", Content: "continue or stop", CreatedAt: at.Add(500 * time.Microsecond)},
		{ID: 3, Seq: 3, Role: "thinking", Content: "first thinking", GenerationID: "3", CreatedAt: at.Add(time.Millisecond)},
		{ID: 4, Seq: 4, Role: "assistant", Content: "first answer", GenerationID: "3", CreatedAt: at.Add(2 * time.Millisecond)},
		{ID: 5, Seq: 5, Role: "thinking", Content: "second thinking", GenerationID: "3", CreatedAt: at.Add(7 * time.Millisecond)},
		{ID: 6, Seq: 6, Role: "assistant", Content: "[Called tools: sapaloq_stop]", GenerationID: "3", CreatedAt: at.Add(8 * time.Millisecond)},
	}
	call := &parse.ToolCall{ID: "stop-1", Name: "sapaloq_stop"}
	events := []bridge.StreamEvent{
		{Kind: bridge.EventToolCall, GenerationID: "3", ToolCall: call, At: at.Add(6 * time.Millisecond)},
		{Kind: bridge.EventToolUpdate, GenerationID: "3", ToolCall: call, Status: "completed", At: at.Add(7 * time.Millisecond)},
	}

	out := mergeTranscriptItems(turns, events)
	if len(out) != 5 {
		t.Fatalf("entries = %d, want 5: %+v", len(out), out)
	}
	wantKinds := []bridge.TranscriptEntryKind{
		bridge.TranscriptUser,
		bridge.TranscriptThinking,
		bridge.TranscriptText,
		bridge.TranscriptThinking,
		bridge.TranscriptTool,
	}
	for i, want := range wantKinds {
		if out[i].Kind != want {
			t.Fatalf("entry %d kind = %q, want %q; transcript=%+v", i, out[i].Kind, want, out)
		}
	}
	if out[1].Text != "first thinking" || out[2].Text != "first answer" || out[3].Text != "second thinking" {
		t.Fatalf("round text order is wrong: %+v", out)
	}
}

func TestCalledToolsNoteContainsExactNamesAndCounts(t *testing.T) {
	content := "done\n\n[Called tools: exec ×2, read_file]"
	if !calledToolsNoteContains(content, "exec") || !calledToolsNoteContains(content, "read_file") {
		t.Fatalf("expected both tools in %q", content)
	}
	if calledToolsNoteContains(content, "read") {
		t.Fatal("partial tool name must not match")
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
