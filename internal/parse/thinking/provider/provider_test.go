package provider

import (
	"testing"
)

func TestParseOpenAIThinkingChannelTag(t *testing.T) {
	got := ParseOpenAIThinking("before<|channel|>analysis<|message|>deep thought<|end|>after")
	if got.Thinking != "deep thought" {
		t.Errorf("thinking: %q", got.Thinking)
	}
	if got.Response != "beforeafter" {
		t.Errorf("response: %q", got.Response)
	}
}

func TestParseOpenAIThinkingLegacyTag(t *testing.T) {
	got := ParseOpenAIThinking("noise¹thinkthink hard⁄think⁄final")
	if got.Thinking == "" {
		t.Errorf("legacy tag must produce thinking: %+v", got)
	}
}

func TestParseOpenAIThinkingNoTags(t *testing.T) {
	got := ParseOpenAIThinking("just a normal response")
	if got.Thinking != "" {
		t.Errorf("plain text must not produce thinking: %+v", got)
	}
	if got.Response != "just a normal response" {
		t.Errorf("response must be preserved: %q", got.Response)
	}
}

func TestParseClaudeThinkingTags(t *testing.T) {
	got := ParseClaudeThinking("pre<thinking>reasoning</thinking>final answer")
	if got.Thinking != "reasoning" {
		t.Errorf("thinking: %q", got.Thinking)
	}
	if got.Response != "prefinal answer" {
		t.Errorf("response: %q", got.Response)
	}
}

func TestParseClaudeFinalTag(t *testing.T) {
	got := ParseClaudeThinking("noise<final>clean answer</final>rest")
	if got.Final != "clean answer" {
		t.Errorf("final: %q", got.Final)
	}
	if got.Response == "" {
		t.Errorf("response must not be empty: %+v", got)
	}
}

func TestParseKimiThinkingFallsBackToLegacy(t *testing.T) {
	// No channel tag, but legacy ¹think⁄think⁄ present.
	got := ParseKimiThinking("hello¹thinkthink deep⁄think⁄world")
	if got.Thinking == "" {
		t.Errorf("kimi fallback must produce thinking: %+v", got)
	}
}

func TestParsedStripForMemory(t *testing.T) {
	cases := []struct {
		name string
		in   Parsed
		want string
	}{
		{"final wins over response", Parsed{Response: "noisy", Final: "clean"}, "clean"},
		{"response when no final", Parsed{Response: "  answer  "}, "answer"},
		{"empty when nothing set", Parsed{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.StripForMemory(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
