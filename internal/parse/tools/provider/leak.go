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

// templateLeakTokens are chat-template control markers some open-weight models
// (notably MiniMax-M3) leak verbatim into the visible content channel when they
// are confused about whether it is their turn to speak or to call a tool. They
// are never part of a real answer and they corrupt inline tool-call
// reassembly (a stray `[/ask]` between two `{...}` objects breaks the scan), so
// the bridge strips them from the visible text before both emit and leak-scan.
var templateLeakTokens = []string{
	"[/assistant]", "[assistant]",
	"[/tool]", "[tool]",
	"[/tool_call]", "[tool_call]",
	"[/ask]", "[ask]",
	"[/plan]", "[plan]",
	"[/agent]", "[agent]",
	"[/system]", "[system]",
	"[/user]", "[user]",
	"<|im_start|>", "<|im_end|>",
	"<|assistant|>", "<|user|>", "<|system|>",
	"<|tool|>", "<|tool_call|>",
	// Anthropic-style tool-use wrapper that some OpenAI-compatible proxies leak
	// around <invoke> blocks. The wrapper itself carries no arguments, so it is
	// safe to strip from visible text. NOTE: we deliberately do NOT list
	// <invoke>/<parameter>/</invoke> here - those ARE the tool call and must
	// survive long enough for scanXMLInvokeFrom to reassemble them; stripping
	// would happen before the leak scan and destroy the call.
	"<function_calls>", "</function_calls>",
}

// StripTemplateLeakTokens removes chat-template control markers (see
// templateLeakTokens) from a visible-content fragment, returning the cleaned
// text. It is a no-op for text that contains none of them (the common case), so
// it is cheap to call on every delta. Stripping happens before leak-scanning so
// a leaked `[/ask]` wedged between two inline tool-call objects no longer
// derails reassembly, and before emit so the marker never reaches the user.
func StripTemplateLeakTokens(text string) string {
	if text == "" {
		return text
	}
	if !strings.ContainsAny(text, "[<") {
		return text
	}
	for _, tok := range templateLeakTokens {
		if strings.Contains(text, tok) {
			text = strings.ReplaceAll(text, tok, "")
		}
	}
	return text
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
// scan the buffer (see ParseToolCallLeakFrom) - scanning one delta at a time
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
	// Two leak shapes are possible in the same buffer: the JSON form this
	// scanner originally handled, and the Anthropic-style <invoke> XML that
	// leaks when an OpenAI-compatible proxy fronting Claude fails to translate
	// tool_use blocks. Run both and return whichever COMPLETES earliest; on a
	// tie of "nothing complete yet" we report the smaller pending frontier so a
	// partial block of either shape is retained for the next delta.
	jsonTC, jsonNext, jsonOK := scanJSONLeakFrom(text, from, known)
	xmlTC, xmlNext, xmlOK := scanXMLInvokeFrom(text, from, known)
	switch {
	case jsonOK && xmlOK:
		// Both completed: emit the one that ends earlier in the buffer so the
		// caller advances past it and re-scans for the other on the next loop.
		if xmlNext < jsonNext {
			return xmlTC, xmlNext, true
		}
		return jsonTC, jsonNext, true
	case jsonOK:
		return jsonTC, jsonNext, true
	case xmlOK:
		return xmlTC, xmlNext, true
	default:
		// Neither completed. Keep the smaller frontier so an in-flight partial
		// of either shape survives into the next feed.
		if xmlNext < jsonNext {
			return parse.ToolCall{}, xmlNext, false
		}
		return parse.ToolCall{}, jsonNext, false
	}
}

// scanJSONLeakFrom is the original JSON/labeled leak scanner, unchanged in
// behaviour. It is split out so ParseToolCallLeakFrom can run it alongside the
// XML scanner and merge their results.
func scanJSONLeakFrom(text string, from int, known func(string) bool) (parse.ToolCall, int, bool) {
	if from < 0 {
		from = 0
	}
	for i := from; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		// A label/name immediately preceding this '{' lets the model use the
		// "labeled" tool-call forms it is actually instructed to emit (see the
		// role prompts): a bracketed `[Tool: <name>]\n{args}` or a bare
		// `<name> {args}`. In both cases the trailing {...} is the *arguments*
		// object, not a {"name":...,"arguments":{...}} envelope. We look back
		// from this '{' for such a label; on a match the '{' is the args body.
		if label, labelStart, hasLabel := toolLabelBefore(text, i, known); hasLabel {
			obj, ok := scanOneJSONObject(text, i)
			if !ok {
				// args object still streaming - keep the partial from the
				// label so the next delta can complete it.
				return parse.ToolCall{}, labelStart, false
			}
			args := normalizeArgs(obj.text)
			return parse.ToolCall{Name: label, Arguments: args, Source: "openai_inline"}, obj.end + 1, true
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
	// No complete object matched. If the buffer ends with a partial bracketed
	// label (an unclosed `[...` whose '{' has not arrived yet), back the scan
	// frontier off to that '[' so the label is retained for the next delta -
	// otherwise we'd advance past it and never recognise the call once its
	// args object finally streams in.
	if open := strings.LastIndexByte(text, '['); open >= from && !strings.ContainsAny(text[open:], "{]") {
		return parse.ToolCall{}, open, false
	}
	return parse.ToolCall{}, len(text), false
}

// toolLabelBefore inspects the bytes immediately preceding the args-object '{'
// at index `brace` for one of the labeled tool-call forms the model is told to
// emit. It recognises:
//
//	[Tool: <name>]   (bracketed label, optional whitespace before the '{')
//	<name>           (a bare known tool-name token, whitespace before the '{')
//
// On a match it returns the tool name and the index where the label begins (so
// a streaming caller can retain the whole label+partial-args for the next
// delta). The bare form requires `known` to confirm the token is a real tool
// name - otherwise any `word {` in prose would be misread. The bracketed form
// is unambiguous enough to accept without `known`, but still honours it when
// provided.
func toolLabelBefore(text string, brace int, known func(string) bool) (name string, start int, ok bool) {
	// Skip whitespace between the label and the '{'.
	j := brace - 1
	for j >= 0 && isASCIISpace(text[j]) {
		j--
	}
	if j < 0 {
		return "", 0, false
	}
	// Bracketed form: ...[Tool: NAME]{
	if text[j] == ']' {
		open := strings.LastIndexByte(text[:j], '[')
		if open < 0 {
			return "", 0, false
		}
		inner := strings.TrimSpace(text[open+1 : j])
		low := strings.ToLower(inner)
		if !strings.HasPrefix(low, "tool:") {
			return "", 0, false
		}
		n := strings.TrimSpace(inner[len("tool:"):])
		if n == "" || !isToolNameToken(n) {
			return "", 0, false
		}
		if known != nil && !known(n) {
			return "", 0, false
		}
		return n, open, true
	}
	// Bare form: ...NAME{ - walk back over a single tool-name token.
	end := j + 1
	k := j
	for k >= 0 && isToolNameByte(text[k]) {
		k--
	}
	tokStart := k + 1
	if tokStart >= end {
		return "", 0, false
	}
	// The token must be a standalone word: preceded by start-of-buffer or a
	// non-identifier byte (whitespace, punctuation). This stops matching the
	// tail of a longer word like "notexec {".
	if tokStart > 0 && isToolNameByte(text[tokStart-1]) {
		return "", 0, false
	}
	n := text[tokStart:end]
	if known == nil || !known(n) {
		return "", 0, false
	}
	return n, tokStart, true
}

// normalizeArgs trims an args object to its canonical bytes. The object came
// from the string-aware scanOneJSONObject, so it is already balanced; we keep
// the raw (trimmed) bytes whether or not json.Valid accepts them so a slightly
// malformed body is still surfaced as a tool call rather than silently dropped.
func normalizeArgs(s string) json.RawMessage {
	// Repair raw control bytes inside string literals so a multi-line args body
	// (e.g. a heredoc) is valid JSON downstream; a no-op for already-valid JSON.
	return json.RawMessage(parse.RepairControlCharsInJSON([]byte(strings.TrimSpace(s))))
}

func isASCIISpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// isToolNameByte reports whether b is a valid character inside a tool-name
// token (letters, digits, underscore - matching the snake_case tool ids used
// throughout SapaLOQ such as read_file, web_search, sapaloq_complete_task).
func isToolNameByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// isToolNameToken reports whether s is composed entirely of tool-name bytes.
func isToolNameToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isToolNameByte(s[i]) {
			return false
		}
	}
	return true
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
// is rejected - silently dropping the tool call. This was the root cause of the
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
	// Repair raw control bytes inside string literals first: a model that
	// writes an inline tool call with a multi-line argument (e.g. a heredoc
	// body) embeds real newlines, which make the JSON technically invalid and
	// would otherwise cause the whole call to be rejected and lost.
	b := parse.RepairControlCharsInJSON([]byte(s))
	if !json.Valid(b) {
		return false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
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
	// Repair raw control bytes (see looksLikeToolJSON) so a multi-line argument
	// value decodes - and so the Arguments we hand downstream are valid JSON
	// that parseToolArgs can unmarshal.
	if err := json.Unmarshal(parse.RepairControlCharsInJSON([]byte(s)), &raw); err != nil {
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
