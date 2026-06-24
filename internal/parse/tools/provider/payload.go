package provider

import (
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// payloadSeparator is the in-band separator used by EncodeToolCallPayload /
// DecodeToolCallPayload. The wire layer uses it to round-trip a name+arguments
// pair through a single string when bridging between Claude's per-block events
// and the canonical parse.ToolCall shape.
const payloadSeparator = "\x00"

// EncodeToolCallPayload packs a tool name + arguments bytes into one string.
// The format is name + payloadSeparator + arguments, which DecodeToolCallPayload
// can parse back. Used internally by the Claude stream handler.
func EncodeToolCallPayload(name string, args []byte) string {
	return name + payloadSeparator + string(args)
}

// DecodeToolCallPayload is the inverse of EncodeToolCallPayload. It returns
// the reconstructed parse.ToolCall and true on success, or (zero, false) when
// `s` does not carry the payload separator. The source label is stamped as
// "claude_inline" - callers should override Source if they need a different
// provenance.
func DecodeToolCallPayload(s string) (parse.ToolCall, bool) {
	idx := strings.Index(s, payloadSeparator)
	if idx < 0 {
		return parse.ToolCall{}, false
	}
	name := strings.TrimSpace(s[:idx])
	raw := s[idx+len(payloadSeparator):]
	if strings.TrimSpace(name) == "" {
		return parse.ToolCall{}, false
	}
	trimmed := strings.TrimSpace(raw)
	var args json.RawMessage
	if json.Valid([]byte(trimmed)) {
		args = json.RawMessage(trimmed)
	} else {
		args = json.RawMessage(raw)
	}
	return parse.ToolCall{Name: name, Arguments: args, Source: "claude_inline"}, true
}
