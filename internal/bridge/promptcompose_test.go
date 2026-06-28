package bridge

import (
	"strings"
	"testing"
)

func TestComposeAgentUserTextFullTurnIncludesSystem(t *testing.T) {
	text := ComposeAgentUserText([]Message{
		{Role: "system", Content: "you are sapaloq"},
		{Role: "user", Content: "hello"},
	}, false)
	for _, part := range []string{"[system]", "you are sapaloq", "[user]", "hello"} {
		if !strings.Contains(text, part) {
			t.Fatalf("full turn missing %q: %q", part, text)
		}
	}
}

func TestComposeAgentUserTextContinuationFromAssistant(t *testing.T) {
	text := ComposeAgentUserText([]Message{
		{Role: "system", Content: "ignored on continuation"},
		{Role: "user", Content: "old"},
		{Role: "assistant", Content: "calling tool"},
		{Role: "tool", Content: "result"},
	}, true)
	if strings.Contains(text, "[system]") || strings.Contains(text, "old") {
		t.Fatalf("continuation should not repeat prior user/system: %q", text)
	}
	for _, part := range []string{"assistant: calling tool", "tool: result"} {
		if !strings.Contains(text, part) {
			t.Fatalf("continuation missing %q: %q", part, text)
		}
	}
}
