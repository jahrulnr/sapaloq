package provider

import (
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func TestWireRole(t *testing.T) {
	cases := map[string]string{
		"system":    "system",
		"user":      "user",
		"assistant": "assistant",
		// Semantic-only roles must collapse to a wire-accepted role. They are
		// model input (observations), so they map to "user" rather than
		// "assistant" — otherwise the model treats them as its own speech.
		"tool":    "user",
		"error":   "user",
		"unknown": "unknown",
	}
	for in, want := range cases {
		if got := wireRole(in); got != want {
			t.Errorf("wireRole(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildOpenAIMessagesMapsToolRole(t *testing.T) {
	msgs := []bridge.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "tool", Content: "observed output"},
		{Role: "error", Content: "boom"},
	}
	out := buildOpenAIMessages(msgs, nil)
	if len(out) != len(msgs) {
		t.Fatalf("expected %d messages, got %d", len(msgs), len(out))
	}
	wantRoles := []string{"system", "user", "assistant", "user", "user"}
	for i, w := range wantRoles {
		if out[i].Role != w {
			t.Errorf("openAI message %d role = %q, want %q", i, out[i].Role, w)
		}
	}
}

func TestBuildClaudeMessagesMapsToolRole(t *testing.T) {
	msgs := []bridge.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "tool", Content: "observed output"},
		{Role: "error", Content: "boom"},
	}
	out := buildClaudeMessages(msgs, nil)
	if len(out) != len(msgs) {
		t.Fatalf("expected %d messages, got %d", len(msgs), len(out))
	}
	wantRoles := []string{"user", "assistant", "user", "user"}
	for i, w := range wantRoles {
		if out[i].Role != w {
			t.Errorf("claude message %d role = %q, want %q", i, out[i].Role, w)
		}
	}
}
