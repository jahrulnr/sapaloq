package parse

import (
	"encoding/json"
	"strings"
)

// RepairControlCharsInJSON makes a JSON byte slice tolerant of RAW (unescaped)
// control characters inside string literals. Models routinely emit tool-call
// arguments whose string values contain real line breaks — e.g. a heredoc body
// `{"command":"cat > f <<X\n<html>\n…\nX"}` where the "\n" are actual newline
// bytes, not the two-character escape. Per the JSON spec a literal control byte
// (U+0000–U+001F) inside a string is INVALID, so encoding/json rejects it with
// `invalid character '\n' in string literal` and the value is lost. SapaLOQ
// then silently parsed empty arguments, the tool reported "command is
// required", and the model — seeing an empty result — concluded its content was
// "stripped/filtered" and burned turns on base64/chunking workarounds.
//
// This walks the bytes and, while INSIDE a string literal, rewrites each raw
// control byte to its valid JSON escape (\n, \r, \t, \b, \f, or \u00XX).
// Structure outside strings is left byte-for-byte intact, and existing escapes
// (\" and \\) are respected so string boundaries are counted correctly. It is a
// no-op for already-valid JSON, so callers can apply it unconditionally or only
// on a first-pass unmarshal error.
func RepairControlCharsInJSON(raw []byte) []byte {
	// Fast path: valid JSON (or input with no control bytes at all) needs no
	// work. json.Valid is cheap relative to a rewrite + reallocation.
	if json.Valid(raw) {
		return raw
	}
	var b strings.Builder
	b.Grow(len(raw) + 16)
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			switch {
			case escaped:
				// Previous byte was a backslash; emit this one verbatim.
				escaped = false
				b.WriteByte(c)
				continue
			case c == '\\':
				escaped = true
				b.WriteByte(c)
				continue
			case c == '"':
				inString = false
				b.WriteByte(c)
				continue
			}
			if c < 0x20 {
				b.WriteString(escapeControl(c))
				continue
			}
			b.WriteByte(c)
			continue
		}
		// Outside a string: copy verbatim, only tracking string entry.
		if c == '"' {
			inString = true
		}
		b.WriteByte(c)
	}
	return []byte(b.String())
}

// escapeControl returns the canonical JSON escape for a control byte.
func escapeControl(c byte) string {
	switch c {
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	default:
		const hex = "0123456789abcdef"
		return `\u00` + string([]byte{hex[(c>>4)&0xF], hex[c&0xF]})
	}
}
