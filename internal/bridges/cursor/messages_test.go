package cursor

import (
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func TestNormalizeCursorWireMessagesSystemToUser(t *testing.T) {
	t.Parallel()
	out := normalizeCursorWireMessages([]bridge.Message{
		{Role: "system", Content: "You are SapaLOQ Ask."},
		{Role: "user", Content: "hey hey"},
	})
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].Role != "user" {
		t.Fatalf("system mapped role = %q, want user", out[0].Role)
	}
	if !strings.HasPrefix(out[0].Content, cursorSystemInstructionsPrefix) {
		t.Fatalf("system content missing prefix: %q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "You are SapaLOQ Ask.") {
		t.Fatalf("system body lost: %q", out[0].Content)
	}
	if out[1].Role != "user" || out[1].Content != "hey hey" {
		t.Fatalf("user turn = %+v", out[1])
	}
}

func TestNormalizeCursorWireMessagesMultipleSystemBlocks(t *testing.T) {
	t.Parallel()
	out := normalizeCursorWireMessages([]bridge.Message{
		{Role: "system", Content: "persona"},
		{Role: "system", Content: "runtime"},
		{Role: "user", Content: "hi"},
	})
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	for i := 0; i < 2; i++ {
		if out[i].Role != "user" {
			t.Fatalf("block %d role = %q", i, out[i].Role)
		}
		if !strings.HasPrefix(out[i].Content, cursorSystemInstructionsPrefix) {
			t.Fatalf("block %d missing prefix", i)
		}
	}
}

func TestNormalizeCursorWireMessagesToolAsUserXML(t *testing.T) {
	t.Parallel()
	out := normalizeCursorWireMessages([]bridge.Message{
		{Role: "assistant", Content: "calling tool"},
		{Role: "tool", Content: "ok output"},
	})
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[1].Role != "user" {
		t.Fatalf("tool role = %q", out[1].Role)
	}
	for _, want := range []string{"<tool_result>", "<result>ok output</result>", "</tool_result>"} {
		if !strings.Contains(out[1].Content, want) {
			t.Fatalf("tool block missing %q in %q", want, out[1].Content)
		}
	}
}

func TestNormalizeCursorWireMessagesSkipsEmptySystem(t *testing.T) {
	t.Parallel()
	out := normalizeCursorWireMessages([]bridge.Message{
		{Role: "system", Content: "   "},
		{Role: "user", Content: "ping"},
	})
	if len(out) != 1 || out[0].Content != "ping" {
		t.Fatalf("got %+v", out)
	}
}
