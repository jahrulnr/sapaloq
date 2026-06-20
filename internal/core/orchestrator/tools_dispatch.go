package orchestrator

import (
	"context"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// runSharedTool handles the read-only assessment + web tools that any mode
// (Ask, Plan, Agent) may call. Returns (result, handled). When handled is
// false the caller falls through to mode-specific handlers.
func runSharedTool(ctx context.Context, call parse.ToolCall) (string, bool) {
	args := parseToolArgs(call.Arguments)
	switch call.Name {
	case "workspace_read_file":
		return toolReadFile(args), true
	case "workspace_search":
		return toolSearch(args), true
	case "workspace_list_dir":
		return toolListDir(args), true
	case "web_fetch":
		return toolWebFetch(ctx, args), true
	case "web_search":
		return toolWebSearch(ctx, args), true
	default:
		return "", false
	}
}
