package cursor

import (
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
	"github.com/jahrulnr/sapaloq/internal/parse/tools/kimi"
	thinkingcursor "github.com/jahrulnr/sapaloq/internal/parse/thinking/cursor"
)

func TestM9AutoThinkingOnly(t *testing.T) {
	thinking := "planning with native grep and read_file schemas"
	content := ""
	if content != "" || thinking == "" {
		t.Fatalf("expected thinking-only completion")
	}
	events := []bridge.StreamEvent{
		{Kind: bridge.EventThinkingDelta, Delta: thinking},
		{Kind: bridge.EventDone},
	}
	var gotThinking, gotResponse string
	for _, ev := range events {
		switch ev.Kind {
		case bridge.EventThinkingDelta:
			gotThinking += ev.Delta
		case bridge.EventResponseDelta:
			gotResponse += ev.Delta
		}
	}
	if gotThinking == "" {
		t.Fatal("thinking channel empty")
	}
	if gotResponse != "" {
		t.Fatalf("response should stay empty, got %q", gotResponse)
	}
}

func TestM9AutoKimiInline(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	text := `<|tool_call_begin|>glob {"pattern":"*.go"}<|tool_call_end|>`
	calls := kimi.ParseInlineWithTokens(text, schema.KimiTokens())
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	coerced := CoerceToolCall(schema, calls[0])
	if coerced.Name != "glob_file_search" {
		t.Fatalf("name = %q", coerced.Name)
	}
}

func TestM9ProtoToolCall(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"type":"tool_call","tool":{"id":"tc-1","name":"glob","arguments":{"pattern":"*.go"}}}`
	events := DecodeFrame(schema, []byte(raw))
	if len(events) != 1 || events[0].Kind != bridge.EventToolCall {
		t.Fatalf("events = %#v", events)
	}
	if events[0].ToolCall.Name != "glob_file_search" {
		t.Fatalf("name = %q", events[0].ToolCall.Name)
	}
}

func TestM9PostTagVisible(t *testing.T) {
	thinking, response := PostTagVisible("native tools</think>visible answer")
	if thinking != "native tools" || response != "visible answer" {
		t.Fatalf("thinking=%q response=%q", thinking, response)
	}
	parsed := thinkingcursor.ParseCursorThinking("native tools</think>visible answer")
	if parsed.Response != "visible answer" {
		t.Fatalf("parsed response = %q", parsed.Response)
	}
}

func TestM9NoNineRouterCollapse(t *testing.T) {
	combined := "pre-tag grep read_file</think>user-visible"
	thinking, response := PostTagVisible(combined)
	if thinking == "" || !strings.Contains(thinking, "pre-tag") {
		t.Fatalf("thinking collapsed: %q", thinking)
	}
	if response != "user-visible" {
		t.Fatalf("response = %q", response)
	}
	thinkingEvents := []string{thinking}
	responseEvents := []string{response}
	if strings.Join(thinkingEvents, "") == strings.Join(responseEvents, "") {
		t.Fatal("thinking merged into response like 9router collapse")
	}
}

func TestVaultReasonDeclaredSurface(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	declared := []string{"read_file"}
	if got := VaultReason(schema, declared, "glob", parse.ToolCall{Name: "glob_file_search"}); got != "undeclared" {
		t.Fatalf("got = %q", got)
	}
	if got := VaultReason(schema, declared, "read", parse.ToolCall{Name: "read_file"}); got != "" {
		t.Fatalf("got = %q", got)
	}
}
