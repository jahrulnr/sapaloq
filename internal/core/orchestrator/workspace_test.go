package orchestrator

import (
	"context"
	"encoding/json"
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

func TestNewChatSessionUsesInstallDefaultWithoutPicker(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	project := filepath.Join(root, "project")
	for _, dir := range []string{workspace, project} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	o := &Orchestrator{stateDir: filepath.Join(root, "state"), workspaceDir: workspace}
	o.persistChatSessionWorkspace("chat-old", project)
	if got := o.actorCWD("chat-new"); got != workspace {
		t.Fatalf("new chat cwd = %q, want install default %q", got, workspace)
	}
	if got := o.actorCWD("task-fresh"); got != workspace {
		t.Fatalf("task cwd = %q, want install default %q", got, workspace)
	}
}

func TestChatSessionLegacyDefaultFileTreatedAsUnset(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	project := filepath.Join(root, "project")
	for _, dir := range []string{workspace, project} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	o := &Orchestrator{stateDir: filepath.Join(root, "state"), workspaceDir: workspace}
	// Legacy pollution: per-chat file pointing at install default.
	if err := os.MkdirAll(o.workspacesDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(workspaceState{CWD: workspace})
	if err := os.WriteFile(o.workspaceStatePath("chat-legacy"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := o.actorCWD("chat-legacy"); got != workspace {
		t.Fatalf("legacy default file cwd = %q, want install default", got)
	}
}

func TestChatSessionExplicitPickerPathPersists(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	other := filepath.Join(root, "other")
	for _, dir := range []string{workspace, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	o := &Orchestrator{stateDir: filepath.Join(root, "state"), workspaceDir: workspace}
	o.persistChatSessionWorkspace("chat-explicit", other)
	if got := o.actorCWD("chat-explicit"); got != other {
		t.Fatalf("explicit chat cwd = %q, want %q", got, other)
	}
}

func TestChatSessionPickerToDefaultRemovesFile(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	project := filepath.Join(root, "project")
	for _, dir := range []string{workspace, project} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	o := &Orchestrator{stateDir: filepath.Join(root, "state"), workspaceDir: workspace}
	o.persistChatSessionWorkspace("chat-a", project)
	o.persistChatSessionWorkspace("chat-a", workspace)
	if _, err := os.Stat(o.workspaceStatePath("chat-a")); !os.IsNotExist(err) {
		t.Fatalf("default picker should remove chat workspace file, stat err=%v", err)
	}
}
