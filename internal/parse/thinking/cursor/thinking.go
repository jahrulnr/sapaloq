package cursor

import "strings"

type Parsed struct {
	Thinking string
	Response string
	Final    string
}

func ParseCursorThinking(text string) Parsed {
	parsed := Parsed{}
	parts := strings.SplitN(text, "</think>", 2)
	if len(parts) == 2 {
		parsed.Thinking = parts[0]
		parsed.Response = parts[1]
	} else {
		parsed.Response = text
	}
	finalParts := strings.SplitN(parsed.Response, "<|final|>", 2)
	if len(finalParts) == 2 {
		parsed.Response = finalParts[0]
		parsed.Final = finalParts[1]
	}
	return parsed
}

func StripForMemory(text string) string {
	parsed := ParseCursorThinking(text)
	if parsed.Final != "" {
		return strings.TrimSpace(parsed.Final)
	}
	return strings.TrimSpace(parsed.Response)
}
