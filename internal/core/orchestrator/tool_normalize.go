package orchestrator

import (
	"sync"

	"github.com/jahrulnr/sapaloq/internal/bridges/cursor"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

var (
	cursorToolSchema     cursor.Schema
	cursorToolSchemaOnce sync.Once
)

// normalizeUpstreamToolCall maps Cursor product/upstream tool names (Glob, Grep,
// glob_file_search, …) to SapaLOQ declared tools before dispatch. Codex dynamic
// tools already use sapaloq.* names and are left unchanged.
func normalizeUpstreamToolCall(call parse.ToolCall) parse.ToolCall {
	if call.Source == "codex" {
		return call
	}
	cursorToolSchemaOnce.Do(func() {
		cursorToolSchema, _ = cursor.LoadSchema()
	})
	return cursor.ResolveToolCall(cursorToolSchema, call)
}
