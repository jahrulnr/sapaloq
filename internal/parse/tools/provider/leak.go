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
//
// NOTE: this scans `text` as a single string, so it only reassembles a tool
// call that is fully present in `text`. During streaming a large inline call is
// split across many content deltas, so callers must accumulate the content and
// scan the buffer (see ParseToolCallLeakFrom) — scanning one delta at a time
// never sees a balanced {...} for a big argument (e.g. a whole HTML file) and
// silently loses it. `known`, when non-nil, restricts matches to recognised
// tool names so a JSON blob inside file content (that merely has name+arguments
// fields) is not misread as a tool call.
func ParseToolCallLeak(text string, known func(string) bool) (parse.ToolCall, bool) {
	tc, _, ok := ParseToolCallLeakFrom(text, 0, known)
	return tc, ok
}

// ParseToolCallLeakFrom is the incremental form: it scans `text` starting at
// byte offset `from` and, on a match, also returns the offset just past the
// matched object so a streaming caller can advance and avoid O(n²) rescans and
// duplicate emits. When nothing matches it returns the offset up to which the
// buffer has been fully scanned for *complete* objects (so the caller can keep
// the unscanned tail for the next delta).
func ParseToolCallLeakFrom(text string, from int, known func(string) bool) (parse.ToolCall, int, bool) {
	if from < 0 {
		from = 0
	}
	for i := from; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		obj, ok := scanOneJSONObject(text, i)
		if !ok {
			// Unbalanced from here to EOF: a larger object is still being
			// streamed. Report the scan frontier as this '{' so the caller
			// keeps the partial object for the next delta.
			return parse.ToolCall{}, i, false
		}
		if !looksLikeToolJSON(obj.text) {
			i = obj.end
			continue
		}
		name, args, decOK := decodeLooseToolJSON(obj.text)
		if !decOK || (known != nil && !known(name)) {
			i = obj.end
			continue
		}
		return parse.ToolCall{Name: name, Arguments: args, Source: "openai_inline"}, obj.end + 1, true
	}
	return parse.ToolCall{}, len(text), false
}

type scannedObject struct {
	text string
	end  int
}

// scanOneJSONObject returns the balanced {...} substring starting at index
// `start` and the index of the closing brace. It is STRING-AWARE: braces that
// appear inside a JSON string literal (and escaped quotes within it) are
// ignored, so a tool argument whose value is a file body containing `{`/`}`
// (HTML/CSS/JS) does not cause the object to close early. Without this, content
// braces miscount depth, the scan ends mid-object, and the (now invalid) JSON
// is rejected — silently dropping the tool call. This was the root cause of the
// "big tool call lost" bug.
func scanOneJSONObject(text string, start int) (scannedObject, bool) {
	depth := 0
	inString := false
	escaped := false
	for j := start; j < len(text); j++ {
		c := text[j]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
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
