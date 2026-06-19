package cursor

import (
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func foldToolName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func (s Schema) upstreamToolSet() map[string]struct{} {
	set := map[string]struct{}{}
	for _, name := range s.Provider.NativeTools {
		set[foldToolName(name)] = struct{}{}
	}
	for alias, canonical := range s.Aliases() {
		set[foldToolName(alias)] = struct{}{}
		set[foldToolName(canonical)] = struct{}{}
	}
	return set
}

func (s Schema) IsUpstreamTool(name string) bool {
	_, ok := s.upstreamToolSet()[foldToolName(name)]
	return ok
}

// VaultReason returns why a tool call should be vaulted, or "" if it is on the declared surface.
func VaultReason(schema Schema, declared []string, rawName string, resolved parse.ToolCall) string {
	resolvedName := resolved.Name
	if len(declared) > 0 {
		for _, tool := range declared {
			if foldToolName(tool) == foldToolName(resolvedName) {
				return ""
			}
		}
		return "undeclared"
	}
	if !schema.IsUpstreamTool(resolvedName) && !schema.IsUpstreamTool(rawName) {
		return "unknown_upstream"
	}
	return ""
}
