package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestDispatchToolResolvesCursorGlobWithGlobPatternArgs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# agents\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]string{
		"glob_pattern":     "**/AGENTS.md",
		"target_directory": dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{}
	got := o.dispatchTool(context.Background(), providerSnapshot{}, ActorRun{
		Foreground:      true,
		ParentSessionID: "chat-1",
		Role:            "orchestrator",
		Tools:           orchestratorTools,
	}, parse.ToolCall{
		Name:      "glob",
		Arguments: args,
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
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("TODO fixme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]string{
		"pattern": "TODO",
		"path":    dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{}
	got := o.dispatchTool(context.Background(), providerSnapshot{}, ActorRun{
		Foreground:      true,
		ParentSessionID: "chat-1",
		Role:            "orchestrator",
		Tools:           orchestratorTools,
	}, parse.ToolCall{
		Name:      "grep",
		Arguments: args,
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
