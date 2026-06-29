package orchestrator

import (
	"context"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func runSharedTool(ctx context.Context, call parse.ToolCall) (string, bool) {
	return (&Orchestrator{}).runSharedTool(ctx, call)
}

// runSharedTool handles the read-only assessment + web + exec tools that any
// mode (Ask, Plan, Agent) may call. Returns (result, handled). When handled
// is false the caller falls through to mode-specific handlers.
//
// Every work tool here honors wait_for_output: when the model passes
// wait_for_output:false, the tool is dispatched into the background job
// registry and this returns immediately with {job_id, status:"queued"}; the
// model later collects the result via the unified `wait` tool (mode=tool).
// When wait_for_output is true/nil, the tool runs inline and returns its
// result (the pre-existing behavior).
func (o *Orchestrator) runSharedTool(ctx context.Context, call parse.ToolCall) (string, bool) {
	args := parseToolArgs(call.Arguments)
	args = o.resolveActorArgs(ctx, args)
	return o.runSharedToolArgs(ctx, call.Name, args)
}

// runSharedToolArgs executes a shared tool from already parsed arguments. The
// actor dispatcher uses this path so malformed-JSON repair and actor argument
// resolution happen exactly once per call.
func (o *Orchestrator) runSharedToolArgs(ctx context.Context, name string, args toolArgs) (string, bool) {
	if run, ok := o.sharedToolRunner(ctx, args, name); ok {
		if args.WaitForOutput != nil && !*args.WaitForOutput {
			return o.spawnBgTool(ctx, name, run), true
		}
		out, _ := run(ctx)
		out = enrichToolResultWithArtifactFingerprint(name, args.Command, out)
		if args.Path != "" && (name == "write_file" || name == "create_file" || name == "edit_file") {
			out = enrichToolResultWithArtifactFingerprint(name, args.Path, out)
		}
		return out, true
	}
	return "", false
}

// sharedToolRunner returns the background-run function for a shared work tool
// (the read-only assessment set, web tools, and exec). Each returned func
// reproduces the inline tool's behavior so a fire-and-forget call collects
// the exact same result the inline path would have returned. Returns
// (nil, false) for non-work tool names so the caller can fall through.
func (o *Orchestrator) sharedToolRunner(ctx context.Context, args toolArgs, name string) (bgJobRun, bool) {
	switch name {
	case "read_file":
		return func(context.Context) (string, error) { return toolReadFile(args), nil }, true
	case "search":
		return func(context.Context) (string, error) { return toolSearch(args), nil }, true
	case "list_dir":
		return func(context.Context) (string, error) { return toolListDir(args), nil }, true
	case "glob":
		return func(context.Context) (string, error) { return toolGlob(args), nil }, true
	case "read_image":
		return func(context.Context) (string, error) { return toolReadImage(args), nil }, true
	case "web_fetch":
		return func(ctx context.Context) (string, error) { return toolWebFetch(ctx, args), nil }, true
	case "web_search":
		return func(ctx context.Context) (string, error) { return o.webSearch(ctx, args), nil }, true
	case "exec":
		return o.execBgRun(args, actorRunID(ctx)), true
	case "write_file":
		return func(context.Context) (string, error) { return toolWriteFile(args, false), nil }, true
	case "create_file":
		return func(context.Context) (string, error) { return toolWriteFile(args, true), nil }, true
	case "edit_file":
		return func(context.Context) (string, error) { return toolEditFile(args), nil }, true
	case "delete_file":
		return func(context.Context) (string, error) { return toolDeleteFile(args), nil }, true
	default:
		return nil, false
	}
}
