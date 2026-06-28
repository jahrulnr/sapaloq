package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestToolsForRoleAlwaysIncludesMandatoryStop(t *testing.T) {
	o := &Orchestrator{cfg: config.Config{
		SubAgents: config.SubAgentsConfig{
			Roles: map[string]config.SubAgentRole{
				"planner": {AllowedTools: []string{"read_file", "write_plan", "read_plan"}},
			},
		},
	}}
	tools := o.toolsForRole("planner")
	if !sliceContains(tools, "sapaloq_stop") {
		t.Fatalf("planner tools missing sapaloq_stop: %v", tools)
	}
}

func TestRoleAllowsMandatoryStopDespiteConfigAllowlist(t *testing.T) {
	o := &Orchestrator{cfg: config.Config{
		SubAgents: config.SubAgentsConfig{
			Roles: map[string]config.SubAgentRole{
				"planner": {AllowedTools: []string{"read_file", "write_plan"}},
			},
		},
	}}
	if !o.roleAllows("planner", "sapaloq_stop") {
		t.Fatal("planner must always allow sapaloq_stop")
	}
	if o.roleAllows("planner", "write_file") {
		t.Fatal("planner must still deny write_file")
	}
}

func TestPlannerSapaloqStopEndsRun(t *testing.T) {
	dir := t.TempDir()
	o := &Orchestrator{
		memoryDir:  dir,
		tasksDir:   filepath.Join(dir, "state", "tasks"),
		workersDir: filepath.Join(dir, "workers"),
		workers:    newWorkerRegistry(filepath.Join(dir, "workers")),
		cfg: config.Config{
			SubAgents: config.SubAgentsConfig{
				Roles: map[string]config.SubAgentRole{
					"planner": {AllowedTools: []string{"read_file", "write_plan"}},
				},
			},
		},
	}
	planner := &taskRecord{ID: "task-plan", Role: "planner", SessionID: "chat-1", Status: "in_progress"}
	var accum strings.Builder
	got := o.dispatchTool(context.Background(), providerSnapshot{}, ActorRun{
		ID: "task-plan", ParentSessionID: "chat-1", Role: "planner",
		Tools: o.toolsForRole("planner"), Record: planner, result: &accum,
	}, parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"plan done"}`)})
	if !got.stop {
		t.Fatalf("sapaloq_stop should stop planner run: %+v", got)
	}
	if planner.Status != "done" {
		t.Fatalf("planner status = %q, want done", planner.Status)
	}
}

func sliceContains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
