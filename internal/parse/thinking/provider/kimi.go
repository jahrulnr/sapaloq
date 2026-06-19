package provider

import "strings"

// ParseKimiThinking splits Kimi-style streamed text. Kimi emits
// `reasoning_content` and `content` as siblings inside the same `delta`, so
// the bridge handles thinking upstream; this parser only handles the rare
// case where reasoning bleeds into the text field (seen with kimi-k2.5
// reasoning-disabled variants when temperature is non-default).
//
// Kimi historically uses the same `¹think⁄think⁄` tags as the cursor stream,
// so we mirror that handling here.
func ParseKimiThinking(text string) Parsed {
	parsed := ParseOpenAIThinking(text)
	if parsed.Thinking != "" {
		return parsed
	}
	open, close := "¹think", "⁄think⁄"
	if !strings.Contains(text, open) {
		return parsed
	}
	parts := strings.SplitN(text, open, 2)
	parsed.Response = parts[0]
	if len(parts) != 2 {
		return parsed
	}
	rest := parts[1]
	if idx := strings.Index(rest, close); idx >= 0 {
		parsed.Thinking = rest[:idx]
		parsed.Response += rest[idx+len(close):]
	} else {
		parsed.Thinking = rest
	}
	return parsed
}
