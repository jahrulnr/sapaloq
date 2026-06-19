package kimi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func ParseInline(text string) []parse.ToolCall {
	return ParseInlineWithTokens(text, defaultKimiTokens())
}

func ParseInlineWithTokens(text string, tokens []string) []parse.ToolCall {
	begin, end := kimiBeginEnd(tokens)
	pattern := fmt.Sprintf(`(?s)%s\s*([a-zA-Z0-9_\-.]+)\s*(\{.*?\})\s*%s`, regexp.QuoteMeta(begin), regexp.QuoteMeta(end))
	re := regexp.MustCompile(pattern)
	matches := re.FindAllStringSubmatch(text, -1)
	out := make([]parse.ToolCall, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		args := json.RawMessage(strings.TrimSpace(m[2]))
		if !json.Valid(args) {
			args = nil
		}
		out = append(out, parse.NewToolCall(name, args, "kimi_inline"))
	}
	return out
}

func defaultKimiTokens() []string {
	return []string{
		"<|tool_call_begin|>",
		"<|tool_call_end|>",
		"<|tool_call_begin|>",
		"<|tool_call_end|>",
	}
}

func kimiBeginEnd(tokens []string) (begin, end string) {
	begin = "<|tool_call_begin|>"
	end = "<|tool_call_end|>"
	for _, t := range tokens {
		if strings.Contains(t, "begin") && strings.Contains(t, "kimi") {
			begin = t
		}
		if strings.Contains(t, "end") && strings.Contains(t, "kimi") && !strings.Contains(t, "begin") {
			end = t
		}
	}
	return begin, end
}
