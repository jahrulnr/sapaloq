package provider

import (
	"strings"

	"github.com/jahrulnr/sapaloq/internal/config"
)

// ParserKind is the wire-format identifier. The provider bridge auto-detects
// it from the entry's parser field, then model name, then endpoint URL.
type ParserKind string

const (
	ParserOpenAI ParserKind = "openai"
	ParserClaude ParserKind = "claude"
	ParserKimi   ParserKind = "kimi"
)

// AuthScheme is the credential header layout the request uses.
type AuthScheme string

const (
	AuthBearer  AuthScheme = "bearer"    // Authorization: Bearer <token>
	AuthXAPIKey AuthScheme = "x-api-key" // x-api-key: <token>
)

// DefaultContextWindow is the fallback when the entry doesn't set one.
// 1M tokens matches the most generous current models (Claude Sonnet 4,
// Gemini 2.5 Pro, GPT-5 family) so the bridge does not truncate by default.
const DefaultContextWindow = 1_000_000

// parserFromString normalises a parser name string into a ParserKind, or
// returns "" when the value is not recognised.
func parserFromString(s string) ParserKind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "openai":
		return ParserOpenAI
	case "claude", "anthropic":
		return ParserClaude
	case "kimi", "moonshot":
		return ParserKimi
	}
	return ""
}

// DetectParser chooses the parser for one provider entry. Order:
//
//  1. Explicit entry.Parser when non-empty.
//  2. Model name sniff — Anthropic/Moonshot family markers.
//  3. Endpoint URL substring.
//  4. Default to openai (covers OpenAI, OpenRouter, TokenRouter, etc.).
func DetectParser(entry config.LLMBridge) ParserKind {
	if explicit := parserFromString(entry.Parser); explicit != "" {
		return explicit
	}
	if p := detectParserFromModel(entry.Model); p != "" {
		return p
	}
	return detectParserFromEndpoint(entry.Endpoint)
}

// detectParserFromModel sniffs the model name for known family markers.
// Anthropic models always contain "claude"/"opus"/"sonnet"/"haiku",
// Moonshot models always contain "kimi"/"moonshot", and everything else
// (gpt, deepseek, qwen, llama, mistral, etc.) is OpenAI-compatible.
func detectParserFromModel(model string) ParserKind {
	lower := strings.ToLower(model)
	for _, marker := range []string{"claude", "opus", "sonnet", "haiku"} {
		if strings.Contains(lower, marker) {
			return ParserClaude
		}
	}
	for _, marker := range []string{"kimi", "moonshot"} {
		if strings.Contains(lower, marker) {
			return ParserKimi
		}
	}
	return ""
}

// detectParserFromEndpoint does the URL-substring fallback for cases where
// the user has not configured Parser or picked a recognisable model name.
func detectParserFromEndpoint(endpoint string) ParserKind {
	lower := strings.ToLower(endpoint)
	switch {
	case strings.Contains(lower, "anthropic"), strings.Contains(lower, "claude"):
		return ParserClaude
	case strings.Contains(lower, "moonshot"), strings.Contains(lower, "kimi"):
		return ParserKimi
	default:
		return ParserOpenAI
	}
}

// DetectAuthScheme picks the credential header. Order:
//
//  1. Explicit entry.AuthScheme (recognised values only).
//  2. Parser-derived: claude → x-api-key, others → bearer.
//
// "anthropic" is accepted as an alias for x-api-key.
func DetectAuthScheme(entry config.LLMBridge, parser ParserKind) AuthScheme {
	if s, ok := parseAuthScheme(entry.AuthScheme); ok {
		return s
	}
	if parser == ParserClaude {
		return AuthXAPIKey
	}
	return AuthBearer
}

// parseAuthScheme normalises a string into an AuthScheme and reports
// whether the value was recognised.
func parseAuthScheme(s string) (AuthScheme, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bearer":
		return AuthBearer, true
	case "x-api-key", "x_api_key", "anthropic":
		return AuthXAPIKey, true
	}
	return "", false
}

// DetectAPIVersion returns the anthropic-version header value. The caller
// is responsible for applying the default ("2023-06-01") when this returns
// empty.
func DetectAPIVersion(entry config.LLMBridge) string {
	return strings.TrimSpace(entry.APIVersion)
}

// DetectContextWindow returns the maximum input size (in tokens) the bridge
// will forward. Returns entry.ContextWindow when > 0, else DefaultContextWindow.
func DetectContextWindow(entry config.LLMBridge) int {
	if entry.ContextWindow > 0 {
		return entry.ContextWindow
	}
	return DefaultContextWindow
}

// DetectMaxTokens returns the per-turn output cap. Zero means "let the
// model use its own default".
func DetectMaxTokens(entry config.LLMBridge) int {
	return entry.MaxTokens
}

// DetectReasoningEffort returns the thinking-intensity hint. Empty means
// "let the model use its own default".
func DetectReasoningEffort(entry config.LLMBridge) string {
	return strings.TrimSpace(entry.ReasoningEffort)
}
