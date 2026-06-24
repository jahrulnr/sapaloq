package provider

import (
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// XML tool-use leak parsing.
//
// Some OpenAI-compatible proxies (Blackbox, OpenRouter, TokenRouter, ...) front
// an Anthropic model (e.g. Claude Opus). When the proxy fails to translate the
// model's native `tool_use` blocks into OpenAI `delta.tool_calls`, the raw
// Anthropic-style XML tool call leaks into the visible content channel:
//
//	<invoke name="exec">
//	<parameter name="command">echo hi</parameter>
//	</invoke>
//
// (optionally wrapped in <function_calls> ... </function_calls>). The JSON leak
// scanner does not recognise this shape, so the call was silently dropped: no
// tool result, the model saw "empty tool output", concluded its tools were
// dead, and gave up mid-task. scanXMLInvokeFrom recovers these calls.

const xmlInvokeOpenPrefix = "<invoke"

// scanXMLInvokeFrom scans `text` from byte offset `from` for the first complete
// `<invoke name="NAME"> ... </invoke>` block and assembles it into a
// parse.ToolCall. The contract mirrors ParseToolCallLeakFrom / scanOneJSONObject:
//
//   - On a complete, accepted match it returns (call, offsetJustPastClose, true).
//   - When an <invoke ...> opener is present but its </invoke> has not arrived
//     yet (mid-stream), it returns (zero, indexOfThatOpener, false) so the
//     streaming caller retains the partial block for the next delta.
//   - When nothing matches it returns (zero, len(text), false).
//
// `known`, when non-nil, restricts matches to recognised tool names so a stray
// <invoke> inside, say, quoted documentation is not misread as a real call.
func scanXMLInvokeFrom(text string, from int, known func(string) bool) (parse.ToolCall, int, bool) {
	if from < 0 {
		from = 0
	}
	search := from
	for {
		open := indexFold(text, xmlInvokeOpenPrefix, search)
		if open < 0 {
			// No complete `<invoke` opener. A streamed opener can be split mid
			// token (e.g. the buffer ends with "<inv"); if we advanced the scan
			// frontier past that '<' it would never be recognised once the rest
			// arrives. Back the frontier off to the last '<' in the tail that
			// could still grow into an opener so the next delta completes it.
			if lt := lastPartialOpener(text, search); lt >= 0 {
				return parse.ToolCall{}, lt, false
			}
			return parse.ToolCall{}, len(text), false
		}
		// Find the end of the opening tag: `<invoke name="X" ...>`.
		gt := strings.IndexByte(text[open:], '>')
		if gt < 0 {
			// Opening tag itself is still streaming. Hold the frontier at the
			// opener so the next delta can complete it.
			return parse.ToolCall{}, open, false
		}
		tagEnd := open + gt // index of '>'
		openTag := text[open : tagEnd+1]

		name, okName := xmlAttr(openTag, "name")
		if !okName || strings.TrimSpace(name) == "" {
			// Not a usable <invoke>; skip past this opener and keep looking.
			search = tagEnd + 1
			continue
		}
		name = strings.TrimSpace(name)

		// Locate the matching close tag.
		closeIdx := indexFold(text, "</invoke>", tagEnd+1)
		if closeIdx < 0 {
			// Body still streaming. Keep the partial block from this opener.
			return parse.ToolCall{}, open, false
		}
		body := text[tagEnd+1 : closeIdx]
		past := closeIdx + len("</invoke>")

		if known != nil && !known(name) {
			// Recognised shape but not a declared tool: skip it (do not treat
			// it as a call) and continue scanning after the block.
			search = past
			continue
		}

		args := xmlParamsToArgs(body)
		return parse.ToolCall{Name: name, Arguments: args, Source: "openai_inline"}, past, true
	}
}

// xmlParamsToArgs walks the <parameter name="KEY">VALUE</parameter> children of
// an <invoke> body (in document order) and assembles them into a JSON
// arguments object. A value that is itself valid JSON (number, bool, object,
// array, quoted string) is kept as-is; anything else is treated as a raw string
// (the common case, e.g. a shell command or a file body).
func xmlParamsToArgs(body string) json.RawMessage {
	type kv struct {
		key string
		raw json.RawMessage
	}
	var pairs []kv
	search := 0
	for {
		open := indexFold(body, "<parameter", search)
		if open < 0 {
			break
		}
		gt := strings.IndexByte(body[open:], '>')
		if gt < 0 {
			break
		}
		tagEnd := open + gt
		openTag := body[open : tagEnd+1]
		key, okKey := xmlAttr(openTag, "name")
		if !okKey || strings.TrimSpace(key) == "" {
			search = tagEnd + 1
			continue
		}
		closeIdx := indexFold(body, "</parameter>", tagEnd+1)
		if closeIdx < 0 {
			break
		}
		val := body[tagEnd+1 : closeIdx]
		pairs = append(pairs, kv{key: strings.TrimSpace(key), raw: coerceParamValue(val)})
		search = closeIdx + len("</parameter>")
	}

	var b strings.Builder
	b.WriteByte('{')
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(p.key)
		b.Write(keyBytes)
		b.WriteByte(':')
		b.Write(p.raw)
	}
	b.WriteByte('}')
	// Repair raw control bytes inside string literals so a multi-line value
	// (e.g. a whole file body) is valid JSON downstream - matching the JSON
	// leak path's normalizeArgs.
	return json.RawMessage(parse.RepairControlCharsInJSON([]byte(b.String())))
}

// coerceParamValue turns a raw <parameter> body into a JSON value. If the
// trimmed body is already a valid JSON scalar/object/array it is kept verbatim;
// otherwise it is emitted as a JSON string. Note the body is NOT trimmed when
// treated as a string except for surrounding whitespace the model commonly adds
// around the value - file/heredoc bodies keep their internal whitespace.
func coerceParamValue(val string) json.RawMessage {
	trimmed := strings.TrimSpace(val)
	if trimmed != "" {
		switch trimmed[0] {
		case '{', '[', '"', 't', 'f', 'n', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			if json.Valid([]byte(trimmed)) {
				return json.RawMessage(trimmed)
			}
		}
	}
	enc, err := json.Marshal(trimmed)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return json.RawMessage(enc)
}

// xmlAttr extracts a double- or single-quoted attribute value from an opening
// tag, e.g. xmlAttr(`<invoke name="exec">`, "name") -> ("exec", true). It is a
// minimal, allocation-light scanner sufficient for the constrained tool-use
// XML; it is not a general XML parser.
func xmlAttr(tag, attr string) (string, bool) {
	idx := indexFold(tag, attr, 0)
	if idx < 0 {
		return "", false
	}
	rest := tag[idx+len(attr):]
	// Skip whitespace then '=' then whitespace.
	i := 0
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
		i++
	}
	if i >= len(rest) || rest[i] != '=' {
		return "", false
	}
	i++
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
		i++
	}
	if i >= len(rest) {
		return "", false
	}
	quote := rest[i]
	if quote != '"' && quote != '\'' {
		return "", false
	}
	i++
	end := strings.IndexByte(rest[i:], quote)
	if end < 0 {
		return "", false
	}
	return rest[i : i+end], true
}

// lastPartialOpener returns the index of the final '<' at or after `from` whose
// remaining bytes are a prefix of "<invoke" (case-insensitive) - i.e. a tag
// opener that may still be streaming in. It returns -1 when the tail's last '<'
// cannot be the start of an <invoke> opener, so a stray '<' in prose does not
// stall the scan frontier forever.
func lastPartialOpener(text string, from int) int {
	if from < 0 {
		from = 0
	}
	lt := strings.LastIndexByte(text[from:], '<')
	if lt < 0 {
		return -1
	}
	abs := from + lt
	tail := strings.ToLower(text[abs:])
	// The tail must be a strict prefix of "<invoke" (we already know there is
	// no complete "<invoke" here). e.g. "<", "<i", "<inv", "<invok".
	if len(tail) < len(xmlInvokeOpenPrefix) && strings.HasPrefix(xmlInvokeOpenPrefix, tail) {
		return abs
	}
	return -1
}

// indexFold is a case-insensitive strings.Index starting at offset `from`.
// Tool-use XML is conventionally lowercase, but proxies are inconsistent, so we
// match case-insensitively to be robust.
func indexFold(s, substr string, from int) int {
	if from < 0 {
		from = 0
	}
	if from > len(s) {
		return -1
	}
	idx := strings.Index(strings.ToLower(s[from:]), strings.ToLower(substr))
	if idx < 0 {
		return -1
	}
	return from + idx
}
