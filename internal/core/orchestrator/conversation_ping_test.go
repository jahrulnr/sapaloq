package orchestrator

import (
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestForegroundStopOnlyTurn(t *testing.T) {
	pending := []scheduledTool{{call: parse.ToolCall{Name: "sapaloq_stop"}}}
	if !foregroundStopOnlyTurn(1, pending, false) {
		t.Fatal("expected pending sapaloq_stop-only turn")
	}
	if foregroundStopOnlyTurn(1, []scheduledTool{{call: parse.ToolCall{Name: "read_file"}}}, false) {
		t.Fatal("read_file should not count as stop-only")
	}
	if !foregroundStopOnlyTurn(1, nil, true) {
		t.Fatal("expected in-bridge stop-only turn")
	}
}

func TestCalledToolsNoteForTurnUsesTracked(t *testing.T) {
	tracked := []toolCallTrace{{call: parse.ToolCall{Name: "sapaloq_stop", ID: "1"}}}
	note := calledToolsNoteForTurn(nil, tracked)
	if note == "" || note != "[Called tools: sapaloq_stop]" {
		t.Fatalf("note = %q", note)
	}
}
