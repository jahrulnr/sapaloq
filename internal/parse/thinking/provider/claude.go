package provider

import "strings"

// ParseClaudeThinking splits a Claude streamed text delta into thinking and
// response. Anthropic already routes thinking text via
// content_block_delta.type=thinking_delta, so this parser only fires when
// reasoning bleeds into a text block (rare, but observed on some upstream
// proxies that collapse thinking + text into a single stream).
//
// Claude doesn't expose an explicit "final" tag, but models occasionally emit
// "<final>...</final>" markers to delimit a clean answer.
func ParseClaudeThinking(text string) Parsed {
	parsed := Parsed{Response: text}
	open, close := "<thinking>", "</thinking>"
	if strings.Contains(text, open) {
		parts := strings.SplitN(text, open, 2)
		parsed.Response = parts[0]
		if len(parts) == 2 {
			rest := parts[1]
			if idx := strings.Index(rest, close); idx >= 0 {
				parsed.Thinking = rest[:idx]
				parsed.Response += rest[idx+len(close):]
			} else {
				parsed.Thinking = rest
				parsed.Response = parts[0]
			}
		}
	}
	if i := strings.Index(parsed.Response, "<final>"); i >= 0 {
		end := strings.Index(parsed.Response[i:], "</final>")
		if end >= 0 {
			parsed.Final = parsed.Response[i+len("<final>") : i+end]
			parsed.Response = parsed.Response[:i] + parsed.Response[i+end+len("</final>"):]
		}
	}
	return parsed
}
