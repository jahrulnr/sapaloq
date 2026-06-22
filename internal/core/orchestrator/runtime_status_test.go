package orchestrator

import (
	"path/filepath"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
)

func TestRuntimeStatusReportsModelPathsAndLiveActors(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Runtime.DataDir = root
	cfg.Events.Bus.SocketPath = filepath.Join(root, "run", "sapaloq.sock")
	o := &Orchestrator{
		cfgPath:      filepath.Join(root, "config.json"),
		cfg:          cfg,
		entry:        config.LLMBridge{Key: "blackbox", Model: "opus", Driver: "provider-bridge"},
		workspaceDir: filepath.Join(root, "workspace"),
		stateDir:     filepath.Join(root, "state"),
		workers:      newWorkerRegistry(filepath.Join(root, "state", "workers")),
	}
	o.workers.register("task-1", "planner", "session-1", "local-default")

	got := o.RuntimeStatus()
	if got.Provider != "blackbox" || got.Model != "opus" {
		t.Fatalf("model status = %+v", got)
	}
	if got.WorkspacePath != filepath.Join(root, "workspace") {
		t.Fatalf("workspace = %q", got.WorkspacePath)
	}
	if len(got.Actors) != 1 || got.Actors[0].Role != "planner" {
		t.Fatalf("actors = %+v", got.Actors)
	}
}

func TestRuntimeStatusHandlesNoWorkerRegistry(t *testing.T) {
	cfg := config.DefaultConfig()
	o := &Orchestrator{cfg: cfg, entry: config.LLMBridge{Key: "cursor", Model: "default"}}
	got := o.RuntimeStatus()
	if len(got.Actors) != 0 {
		t.Fatalf("unexpected actors: %+v", got.Actors)
	}
	if got.Model != "default" {
		t.Fatalf("model = %q", got.Model)
	}
}
