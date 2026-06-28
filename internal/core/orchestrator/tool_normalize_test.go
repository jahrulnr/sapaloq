package orchestrator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestDispatchToolResolvesCursorGlobWithGlobPatternArgs(t *testing.T) {
	o := &Orchestrator{}
	got := o.dispatchTool(context.Background(), providerSnapshot{}, ActorRun{
		Foreground:      true,
		ParentSessionID: "chat-1",
		Role:            "ask",
		Tools:           askTools,
	}, parse.ToolCall{
		Name:      "glob",
		Arguments: json.RawMessage(`{"glob_pattern":"**/AGENTS.md","target_directory":"/tmp"}`),
		Source:    "openai_inline",
	})
	if !got.handled {
		t.Fatalf("glob should dispatch, got unhandled: %+v", got)
	}
	if got.text == "Error: pattern is required." {
		t.Fatalf("glob_pattern must remap to pattern, got: %q", got.text)
	}
}

func TestDispatchToolResolvesCursorGrepToSearch(t *testing.T) {
	o := &Orchestrator{}
	got := o.dispatchTool(context.Background(), providerSnapshot{}, ActorRun{
		Foreground:      true,
		ParentSessionID: "chat-1",
		Role:            "ask",
		Tools:           askTools,
	}, parse.ToolCall{
		Name:      "grep",
		Arguments: json.RawMessage(`{"pattern":"TODO","path":"/tmp"}`),
		Source:    "openai_inline",
	})
	if !got.handled {
		t.Fatalf("grep should dispatch as search, got unhandled: %+v", got)
	}
}

func TestNormalizeUpstreamToolCallPreservesCodex(t *testing.T) {
	call := parse.ToolCall{Name: "sapaloq_stop", Source: "codex"}
	if got := normalizeUpstreamToolCall(call); got.Name != "sapaloq_stop" {
		t.Fatalf("codex call = %q", got.Name)
	}
}
