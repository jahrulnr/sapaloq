package provider

import "strings"

// ParseOpenAIThinking splits an OpenAI-style streamed text into thinking and
// response. For Chat Completions the reasoning content is already routed to
// the bridge's EventThinkingDelta channel via `delta.reasoning_content`, so
// this parser only handles fallback cases:
//
//   - Embedded <|channel|>analysis<|message|>...<|end|> tags
//   - Legacy ¹think⁄ XML tags
func ParseOpenAIThinking(text string) Parsed {
	parsed := Parsed{Response: text}
	open, close := "<|channel|>analysis<|message|>", "<|end|>"
	if strings.Contains(text, open) {
		parts := strings.SplitN(text, open, 2)
		parsed.Response = parts[0] // text before the channel tag stays in the response
		if len(parts) == 2 {
			rest := parts[1]
			if idx := strings.Index(rest, close); idx >= 0 {
				parsed.Thinking = rest[:idx]
				parsed.Response += rest[idx+len(close):]
			} else {
				// Unclosed channel — treat the rest as pending thinking.
				parsed.Thinking = rest
				parsed.Response = parts[0]
			}
		}
	} else if i := strings.Index(text, "¹think"); i >= 0 {
		parsed.Response = text[:i]
		rest := text[i+len("¹think"):]
		if end := strings.Index(rest, "⁄think⁄"); end >= 0 {
			parsed.Thinking = rest[:end]
			parsed.Response += rest[end+len("⁄think⁄"):]
		} else {
			parsed.Thinking = rest
		}
	}
	return parsed
}
