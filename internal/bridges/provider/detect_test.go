package provider

import (
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
)

// entryWithParser builds a single LLMBridge entry with the given parser
// value, used to test parser/auth/version detection in isolation.
func entryWithParser(parser string) config.LLMBridge {
	return config.LLMBridge{Parser: parser, Endpoint: "https://example.com"}
}

func entryWithAuthScheme(scheme string) config.LLMBridge {
	return config.LLMBridge{AuthScheme: scheme, Endpoint: "https://example.com"}
}

func entryWithAPIVersion(v string) config.LLMBridge {
	return config.LLMBridge{APIVersion: v, Endpoint: "https://example.com"}
}

func entryWithContextWindow(w int) config.LLMBridge {
	return config.LLMBridge{ContextWindow: w, Endpoint: "https://example.com"}
}

func TestDetectParserFromEntry(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		want  ParserKind
	}{
		{"openai", "openai", ParserOpenAI},
		{"claude", "claude", ParserClaude},
		{"kimi", "kimi", ParserKimi},
		{"empty falls back to endpoint default", "", ParserOpenAI},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectParser(entryWithParser(tc.entry))
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestDetectParserFromModelName(t *testing.T) {
	cases := []struct {
		model string
		want  ParserKind
	}{
		{"claude-opus-4.8", ParserClaude},
		{"anthropic/claude-3.5-sonnet", ParserClaude},
		{"opus-4.8", ParserClaude},
		{"kimi-k2.6", ParserKimi},
		{"moonshot-v1-128k", ParserKimi},
		// Non-Anthropic/Moonshot models return "" - the caller falls through
		// to endpoint detection or the default.
		{"gpt-4o-mini", ""},
		{"MiniMax-M3", ""},
		{"deepseek-chat", ""},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			entry := config.LLMBridge{Model: tc.model, Endpoint: "https://example.com"}
			got := detectParserFromModel(entry.Model)
			if got != tc.want {
				t.Fatalf("model %q want %q, got %q", tc.model, tc.want, got)
			}
		})
	}
}

func TestDetectAuthSchemeFromEntry(t *testing.T) {
	if got := DetectAuthScheme(entryWithParser("claude"), ParserClaude); got != AuthXAPIKey {
		t.Fatalf("claude must use x-api-key, got %q", got)
	}
	if got := DetectAuthScheme(entryWithParser("openai"), ParserOpenAI); got != AuthBearer {
		t.Fatalf("openai must use bearer, got %q", got)
	}
	if got := DetectAuthScheme(entryWithParser("kimi"), ParserKimi); got != AuthBearer {
		t.Fatalf("kimi must use bearer, got %q", got)
	}
}

func TestDetectAuthSchemeExplicit(t *testing.T) {
	// Explicit AuthScheme wins over parser-derived default
	if got := DetectAuthScheme(entryWithAuthScheme("x-api-key"), ParserOpenAI); got != AuthXAPIKey {
		t.Fatalf("explicit x-api-key must win, got %q", got)
	}
	// "anthropic" alias normalises to x-api-key
	if got := DetectAuthScheme(entryWithAuthScheme("anthropic"), ParserOpenAI); got != AuthXAPIKey {
		t.Fatalf("'anthropic' alias must resolve to x-api-key, got %q", got)
	}
}

func TestDetectAPIVersion(t *testing.T) {
	if got := DetectAPIVersion(entryWithAPIVersion("")); got != "" {
		t.Fatalf("empty entry must return empty, got %q", got)
	}
	if got := DetectAPIVersion(entryWithAPIVersion("2024-01-01")); got != "2024-01-01" {
		t.Fatalf("custom api version overridden: %s", got)
	}
}

func TestDetectContextWindow(t *testing.T) {
	if got := DetectContextWindow(config.LLMBridge{}); got != DefaultContextWindow {
		t.Fatalf("default must be 1M, got %d", got)
	}
	if got := DetectContextWindow(entryWithContextWindow(200_000)); got != 200_000 {
		t.Fatalf("entry context window must win, got %d", got)
	}
}

func TestDetectMaxTokens(t *testing.T) {
	if got := DetectMaxTokens(config.LLMBridge{}); got != 0 {
		t.Fatalf("default must be zero, got %d", got)
	}
	entry := config.LLMBridge{MaxTokens: 4096}
	if got := DetectMaxTokens(entry); got != 4096 {
		t.Fatalf("entry max tokens must win, got %d", got)
	}
}

func TestDetectReasoningEffort(t *testing.T) {
	if got := DetectReasoningEffort(config.LLMBridge{}); got != "" {
		t.Fatalf("default must be empty, got %q", got)
	}
	entry := config.LLMBridge{ReasoningEffort: "high"}
	if got := DetectReasoningEffort(entry); got != "high" {
		t.Fatalf("entry reasoning must win, got %q", got)
	}
}
