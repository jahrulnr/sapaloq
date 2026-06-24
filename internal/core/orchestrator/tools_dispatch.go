package orchestrator

import (
	"context"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func runSharedTool(ctx context.Context, call parse.ToolCall) (string, bool) {
	return (&Orchestrator{}).runSharedTool(ctx, call)
}

// runSharedTool handles the read-only assessment + web tools that any mode
// (Ask, Plan, Agent) may call. Returns (result, handled). When handled is
// false the caller falls through to mode-specific handlers.
func (o *Orchestrator) runSharedTool(ctx context.Context, call parse.ToolCall) (string, bool) {
	args := parseToolArgs(call.Arguments)
	args = o.resolveActorArgs(ctx, args)
	switch call.Name {
	case "read_file":
		return toolReadFile(args), true
	case "search":
		return toolSearch(args), true
	case "list_dir":
		return toolListDir(args), true
	case "glob":
		return toolGlob(args), true
	case "read_image":
		return toolReadImage(args), true
	case "exec":
		return o.toolExec(ctx, args), true
	case "exec_async":
		return o.toolExecAsync(ctx, args), true
	case "exec_status":
		return o.toolExecStatus(ctx, args), true
	case "exec_result":
		return o.toolExecResult(ctx, args), true
	case "exec_cancel":
		return o.toolExecCancel(ctx, args), true
	case "web_fetch":
		return toolWebFetch(ctx, args), true
	case "web_search":
		return toolWebSearch(ctx, args), true
	default:
		return "", false
	}
}
