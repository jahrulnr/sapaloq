package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActorWorkspacePersistsCDAndResolvesRelativeFiles(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	project := filepath.Join(workspace, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{stateDir: filepath.Join(root, "state"), workspaceDir: workspace}
	ctx := withActorRunID(context.Background(), "agent-1")

	if got := o.toolExec(ctx, toolArgs{Command: "cd project && pwd"}); !strings.Contains(got, project) {
		t.Fatalf("cd output = %q, want %q", got, project)
	}
	if got := o.actorCWD("agent-1"); got != project {
		t.Fatalf("persisted cwd = %q, want %q", got, project)
	}
	args := o.resolveActorArgs(ctx, toolArgs{Path: "note.md"})
	if args.Path != filepath.Join(project, "note.md") {
		t.Fatalf("relative path = %q", args.Path)
	}
}

func TestActorWorkspaceIsIsolatedPerActor(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{stateDir: filepath.Join(root, "state"), workspaceDir: workspace}
	o.toolExec(withActorRunID(context.Background(), "agent-a"), toolArgs{Command: "cd a"})

	if got := o.actorCWD("agent-a"); got != filepath.Join(workspace, "a") {
		t.Fatalf("agent-a cwd = %q", got)
	}
	if got := o.actorCWD("agent-b"); got != workspace {
		t.Fatalf("agent-b inherited another actor cwd: %q", got)
	}
}

func TestActorWorkspaceFallsBackWhenPersistedDirectoryDisappears(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	project := filepath.Join(workspace, "gone")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{stateDir: filepath.Join(root, "state"), workspaceDir: workspace}
	o.persistActorCWD("agent-1", project)
	if err := os.RemoveAll(project); err != nil {
		t.Fatal(err)
	}
	if got := o.actorCWD("agent-1"); got != workspace {
		t.Fatalf("missing cwd fallback = %q, want %q", got, workspace)
	}
}

func TestActorWorkspacePersistsFinalCWDWhenLaterCommandFails(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	project := filepath.Join(workspace, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{stateDir: filepath.Join(root, "state"), workspaceDir: workspace}
	ctx := withActorRunID(context.Background(), "agent-1")
	got := o.toolExec(ctx, toolArgs{Command: "cd project && false"})
	if !strings.Contains(got, "exited with error") {
		t.Fatalf("expected command failure, got %q", got)
	}
	if cwd := o.actorCWD("agent-1"); cwd != project {
		t.Fatalf("final cwd after failure = %q, want %q", cwd, project)
	}
}
