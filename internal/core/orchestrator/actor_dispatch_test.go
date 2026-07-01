package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestDispatchToolRoleMatrix(t *testing.T) {
	dir := t.TempDir()
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{}}
	o := &Orchestrator{
		memoryDir:  dir,
		tasksDir:   filepath.Join(dir, "state", "tasks"),
		workersDir: filepath.Join(dir, "workers"),
		workers:    newWorkerRegistry(filepath.Join(dir, "workers")),
		progress:   newAsyncProgressWriter(ProgressWriter{Dir: filepath.Join(dir, "rollout")}),
		cfg:        config.Config{},
		bridge:     fake,
		entry:      config.LLMBridge{Key: "k", Model: "m"},
	}
	ctx := context.Background()
	snap := providerSnapshot{cfg: config.Config{}, entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	out := make(chan bridge.StreamEvent, 4)

	spawn := parse.ToolCall{Name: "sapaloq_spawn_agent", Arguments: []byte(`{"task":"build"}`)}
	got := o.dispatchTool(ctx, snap, ActorRun{
		Foreground: true, ParentSessionID: "chat-1", Role: "orchestrator", Tools: orchestratorTools,
		TaskText: "build", Out: out,
	}, spawn)
	if !got.handled || !strings.Contains(got.text, "background") {
		t.Fatalf("ask spawn_agent = %+v", got)
	}

	exec := parse.ToolCall{Name: "exec", Arguments: []byte(`{"command":"printf ok"}`)}
	planner := &taskRecord{ID: "task-plan", Role: "planner", SessionID: "chat-1"}
	got = o.dispatchTool(ctx, snap, ActorRun{
		ID: "task-plan", ParentSessionID: "chat-1", Role: "planner",
		Tools: o.toolsForRole("planner"), Record: planner,
	}, exec)
	if !got.handled || !strings.Contains(got.text, "ok") {
		t.Fatalf("planner exec = %+v", got)
	}

	scribe := &taskRecord{ID: "task-scribe", Role: "scribe", SessionID: "chat-1"}
	got = o.dispatchTool(ctx, snap, ActorRun{
		ID: "task-scribe", ParentSessionID: "chat-1", Role: "scribe",
		Tools: o.toolsForRole("scribe"), Record: scribe,
	}, exec)
	if !strings.Contains(got.text, "not allowed for role scribe") {
		t.Fatalf("scribe exec should be denied, got %+v", got)
	}

	complete := parse.ToolCall{Name: "sapaloq_complete_task", Arguments: []byte(`{"summary":"done"}`)}
	runner := &taskRecord{ID: "task-run", Role: "task-runner", SessionID: "chat-1"}
	var accum strings.Builder
	got = o.dispatchTool(ctx, snap, ActorRun{
		ID: "task-run", ParentSessionID: "chat-1", Role: "task-runner",
		Tools: o.toolsForRole("task-runner"), Record: runner, result: &accum,
	}, complete)
	if !got.stop || runner.Status != "done" {
		t.Fatalf("complete_task = %+v status=%s", got, runner.Status)
	}
}
