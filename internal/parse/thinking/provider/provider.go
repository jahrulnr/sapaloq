package provider

import "strings"

// Parsed mirrors parse/thinking/cursor.Parsed so providers can be used with
// the same memory-stripping helpers without depending on the cursor package.
type Parsed struct {
	Thinking string
	Response string
	Final    string
}

// StripForMemory returns the part of the streamed text that's safe to keep in
// long-term memory: Final when present, else Response. Thinking is always
// discarded.
func (p Parsed) StripForMemory() string {
	if p.Final != "" {
		return strings.TrimSpace(p.Final)
	}
	return strings.TrimSpace(p.Response)
}
