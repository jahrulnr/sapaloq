package provider

import (
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// StripReasoningFromText walks a text delta looking for embedded analysis
// channels (some legacy OpenAI-style models emit <|channel|>analysis<|message|>
// tags inside delta.content) and returns (thinking, cleanedText). If no
// channel is present the cleaned text equals the input.
func StripReasoningFromText(text string) (thinking, cleaned string) {
	const (
		channelOpen  = "<|channel|>analysis<|message|>"
		channelClose = "<|end|>"
	)
	if !strings.Contains(text, channelOpen) {
		return "", text
	}
	pre, rest := splitOnce(text, channelOpen)
	post, after := splitOnce(rest, channelClose)
	return post, pre + after
}

// HasReasoningLeak returns true when `text` contains markers that suggest the
// model emitted thinking where it shouldn't have. The bridge uses this to
// surface EventToolLeak rather than silently swallowing the content.
func HasReasoningLeak(text string) bool {
	markers := []string{
		"<|channel|>analysis<|message|>",
		"<|reasoning|>",
		"<|thinking|>",
		"¹think",
	}
	for _, m := range markers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// ParseToolCallLeak inspects `text` for inline JSON tool calls that some
// models emit in their content when tool calling is unsupported. Returns the
// first well-formed {"name":..., "arguments":{...}} object it finds, or
// (parse.ToolCall{}, false) when none is present. Source is "openai_inline".
func ParseToolCallLeak(text string) (parse.ToolCall, bool) {
	for _, candidate := range scanJSONObjects(text) {
		if !looksLikeToolJSON(candidate) {
			continue
		}
		name, args, ok := decodeLooseToolJSON(candidate)
		if !ok {
			continue
		}
		return parse.ToolCall{Name: name, Arguments: args, Source: "openai_inline"}, true
	}
	return parse.ToolCall{}, false
}

// scanJSONObjects walks `text` and returns each balanced top-level {...}
// substring. Used by ParseToolCallLeak.
func scanJSONObjects(text string) []string {
	out := make([]string, 0, 4)
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		if obj, ok := scanOneJSONObject(text, i); ok {
			out = append(out, obj.text)
			i = obj.end
		}
	}
	return out
}

type scannedObject struct {
	text string
	end  int
}

// scanOneJSONObject returns the balanced {...} substring starting at index
// `start` and the index of the closing brace.
func scanOneJSONObject(text string, start int) (scannedObject, bool) {
	depth := 0
	for j := start; j < len(text); j++ {
		switch text[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return scannedObject{text: text[start : j+1], end: j}, true
			}
		}
	}
	return scannedObject{}, false
}

// splitOnce splits s at the first occurrence of sep.
func splitOnce(s, sep string) (before, after string) {
	idx := strings.Index(s, sep)
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx+len(sep):]
}

// looksLikeToolJSON returns true when the candidate looks like a tool call:
// contains a string "name" field and either an "arguments" object or a
// "parameters" object.
func looksLikeToolJSON(s string) bool {
	if !json.Valid([]byte(s)) {
		return false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &probe); err != nil {
		return false
	}
	if _, ok := probe["name"]; !ok {
		return false
	}
	_, hasArgs := probe["arguments"]
	_, hasParams := probe["parameters"]
	return hasArgs || hasParams
}

// decodeLooseToolJSON parses {"name": "...", "arguments": {...}} or
// {"name": "...", "parameters": {...}} into a (name, args) pair. We normalise
// "parameters" to "arguments" so downstream consumers don't need to care which
// one the upstream used.
func decodeLooseToolJSON(s string) (name string, args json.RawMessage, ok bool) {
	var raw struct {
		Name       string          `json:"name"`
		Arguments  json.RawMessage `json:"arguments"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return "", nil, false
	}
	if strings.TrimSpace(raw.Name) == "" {
		return "", nil, false
	}
	out := raw.Arguments
	if len(out) == 0 {
		out = raw.Parameters
	}
	if len(out) == 0 {
		out = json.RawMessage("{}")
	}
	return raw.Name, out, true
}
