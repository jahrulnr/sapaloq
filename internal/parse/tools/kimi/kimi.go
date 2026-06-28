package kimi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// ExtractResult holds parsed tool calls and visible text with Kimi markers stripped.
type ExtractResult struct {
	Calls       []parse.ToolCall
	CleanedText string
}

func ParseInline(text string) []parse.ToolCall {
	return ParseInlineWithTokens(text, defaultKimiTokens())
}

func ExtractInline(text string) ExtractResult {
	return ExtractWithTokens(text, defaultKimiTokens())
}

func ParseInlineWithTokens(text string, tokens []string) []parse.ToolCall {
	return ExtractWithTokens(text, tokens).Calls
}

func ExtractWithTokens(text string, tokens []string) ExtractResult {
	if strings.TrimSpace(text) == "" {
		return ExtractResult{}
	}
	normalized := normalizeSpacedTokens(text)
	if !hasToolMarkers(normalized, tokens) {
		return ExtractResult{CleanedText: text}
	}

	calls := parseJSONInline(normalized, tokens)
	if len(calls) == 0 {
		calls = parseSectionInline(normalized)
	}
	cleaned := stripToolTokens(normalized)
	if cleaned == strings.TrimSpace(text) && len(calls) > 0 {
		cleaned = strings.TrimSpace(stripToolTokens(normalized))
	}
	return ExtractResult{Calls: calls, CleanedText: cleaned}
}

func parseJSONInline(text string, tokens []string) []parse.ToolCall {
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

var (
	toolCallBeginRE = regexp.MustCompile(`(?s)<\|(?:redacted_tool_call_begin(?:_kimi)?|tool_call_begin)\|>\s*`)
	toolCallEndRE   = regexp.MustCompile(`<\|(?:redacted_tool_call_end(?:_kimi)?|tool_call_end)\|>`)
	toolSepRE       = regexp.MustCompile(`<\|(?:tool_sep|redacted_tool_sep|redacted_tool_call_argument_begin)\|>\s*`)
	sectionStripRE  = regexp.MustCompile(`(?s)<\|(?:tool_calls_begin|tool_calls_end|tool_call_begin|tool_call_end|tool_calls_section_begin|tool_calls_section_end|redacted_tool_calls_begin|redacted_tool_calls_end|redacted_tool_calls_section_begin|redacted_tool_calls_section_end|redacted_tool_call_begin|redacted_tool_call_end)\|>`)
	sectionRE       = regexp.MustCompile(`(?s)<\|(?:tool_calls_begin|redacted_tool_calls_begin)\|>[\s\S]*?<\|(?:tool_calls_end|redacted_tool_calls_end)\|>`)
	spacedTokenRE   = regexp.MustCompile(`<\s*\|\s*([a-z0-9_]+)\s*\|\s*>`)
)

func parseSectionInline(text string) []parse.ToolCall {
	var out []parse.ToolCall
	rest := text
	for rest != "" {
		loc := toolCallBeginRE.FindStringIndex(rest)
		if loc == nil {
			break
		}
		segment := rest[loc[1]:]
		endLoc := toolCallEndRE.FindStringIndex(segment)
		var body string
		if endLoc != nil {
			body = segment[:endLoc[0]]
			rest = segment[endLoc[1]:]
		} else {
			next := toolCallBeginRE.FindStringIndex(segment)
			if next != nil && next[0] > 0 {
				body = segment[:next[0]]
				rest = segment[next[0]:]
			} else {
				body = segment
				rest = ""
			}
		}
		name, rawArgs := splitToolBody(body)
		if name != "" {
			args := parseToolArguments(name, rawArgs)
			out = append(out, parse.NewToolCall(name, args, "kimi_inline"))
		}
	}
	return out
}

func splitToolBody(body string) (name, args string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", ""
	}
	loc := toolSepRE.FindStringIndex(body)
	if loc == nil {
		parts := strings.Fields(body)
		if len(parts) >= 2 && parts[1] == "command" {
			return parts[0], strings.TrimSpace(body[len(parts[0]):])
		}
		if len(parts) > 0 {
			return parts[0], ""
		}
		return "", ""
	}
	return strings.TrimSpace(body[:loc[0]]), strings.TrimSpace(body[loc[1]:])
}

func parseToolArguments(toolName, raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return json.RawMessage("{}")
	}
	if strings.HasPrefix(raw, "{") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err == nil {
			if inner, ok := obj["input"].(string); ok && shouldParseMultiline(inner) {
				if kv := parseMultilineKeyValue(inner); len(kv) > 0 {
					b, _ := json.Marshal(kv)
					return b
				}
			}
			b, _ := json.Marshal(obj)
			return b
		}
	}
	if shouldParseMultiline(raw) {
		if kv := parseMultilineKeyValue(raw); len(kv) > 0 {
			b, _ := json.Marshal(kv)
			return b
		}
	}
	cmdPart := raw
	if idx := strings.Index(strings.ToLower(cmdPart), "<|tool_sep|>"); idx >= 0 {
		head := strings.ToLower(cmdPart[:idx])
		if strings.Contains(head, "description") || strings.Contains(head, "is_background") {
			cmdPart = strings.TrimSpace(cmdPart[:idx])
		}
	}
	trimmed := strings.TrimSpace(cmdPart)
	if strings.HasPrefix(strings.ToLower(trimmed), "command") {
		command := strings.TrimSpace(trimmed[len("command"):])
		command = strings.TrimLeft(command, "\n")
		if command != "" {
			b, _ := json.Marshal(map[string]string{"command": command})
			return b
		}
	}
	switch toolName {
	case "exec", "Bash", "run_terminal_cmd", "Shell":
		b, _ := json.Marshal(map[string]string{"command": trimmed})
		return b
	}
	b, _ := json.Marshal(map[string]string{"input": trimmed})
	return b
}

func shouldParseMultiline(raw string) bool {
	raw = normalizeDelimiters(raw)
	if toolSepRE.MatchString(raw) {
		return true
	}
	return regexp.MustCompile(`(?m)^[a-zA-Z_][\w.-]*\n`).MatchString(raw)
}

func parseMultilineKeyValue(raw string) map[string]any {
	parts := toolSepRE.Split(normalizeDelimiters(strings.TrimSpace(raw)), -1)
	out := map[string]any{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		nl := strings.Index(part, "\n")
		if nl <= 0 {
			continue
		}
		key := strings.TrimSpace(part[:nl])
		value := strings.TrimSpace(part[nl+1:])
		if key == "" {
			continue
		}
		out[key] = coerceArgValue(key, value)
	}
	return out
}

func coerceArgValue(key, value string) any {
	switch {
	case regexp.MustCompile(`(?i)^(limit|maxResults|max_results|count|offset|top|size|max_tokens|timeout|port|depth)$`).MatchString(key):
		if n, err := strconv.ParseFloat(value, 64); err == nil {
			return n
		}
	case strings.EqualFold(value, "true"), strings.EqualFold(value, "false"):
		return strings.EqualFold(value, "true")
	}
	return value
}

func stripToolTokens(text string) string {
	cleaned := sectionRE.ReplaceAllString(text, "")
	cleaned = sectionStripRE.ReplaceAllString(cleaned, "")
	cleaned = toolCallBeginRE.ReplaceAllString(cleaned, "")
	cleaned = toolCallEndRE.ReplaceAllString(cleaned, "")
	cleaned = toolSepRE.ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

// ToolBlockActive reports whether accumulated visible text contains Kimi tool syntax.
func ToolBlockActive(text string, tokens []string) bool {
	return hasToolMarkers(text, tokens)
}

func hasToolMarkers(text string, tokens []string) bool {
	normalized := normalizeSpacedTokens(text)
	for _, t := range tokens {
		if strings.Contains(normalized, normalizeDelimiters(t)) {
			return true
		}
	}
	return toolCallBeginRE.MatchString(normalized) || sectionRE.MatchString(normalized)
}

func normalizeDelimiters(text string) string {
	text = strings.ReplaceAll(text, "\uFF5C", "|")
	text = strings.ReplaceAll(text, "\u2502", "|")
	text = strings.ReplaceAll(text, "\u2581", "_")
	text = strings.ReplaceAll(text, "\u2017", "_")
	return text
}

func normalizeSpacedTokens(text string) string {
	text = normalizeDelimiters(text)
	return spacedTokenRE.ReplaceAllString(text, "<|$1|>")
}

// DefaultTokens returns Kimi inline tool marker tokens (exported for bridge arg normalization).
func DefaultTokens() []string {
	return defaultKimiTokens()
}

func defaultKimiTokens() []string {
	return []string{
		"<|tool_calls_begin|>",
		"<|tool_calls_end|>",
		"<｜tool▁calls▁begin｜>",
		"<｜tool▁calls▁end｜>",
		"<|tool_call_begin|>",
		"<|tool_call_end|>",
		"<|tool_calls_section_begin|>",
		"<｜tool▁call▁begin｜>",
		"<｜tool▁call▁end｜>",
		"<|tool_sep|>",
		"<｜tool▁sep｜>",
		"<|tool_call_argument_begin|>",
	}
}

func kimiBeginEnd(tokens []string) (begin, end string) {
	begin = "<|tool_call_begin|>"
	end = "<|tool_call_end|>"
	for _, t := range tokens {
		nt := normalizeDelimiters(t)
		if strings.Contains(nt, "begin") && strings.Contains(nt, "kimi") {
			begin = nt
		}
		if strings.Contains(nt, "end") && strings.Contains(nt, "kimi") && !strings.Contains(nt, "begin") {
			end = nt
		}
	}
	return begin, end
}
