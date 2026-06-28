package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestSetSessionWorkspacePersistsActorCWD(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	project := filepath.Join(root, "project")
	for _, dir := range []string{workspace, project} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store, err := chatstore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sessionID, err := store.ActiveSession(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{
		cfg:          config.DefaultConfig(),
		chat:         store,
		entry:        config.LLMBridge{Key: "p", Model: "m"},
		workspaceDir: workspace,
		stateDir:     filepath.Join(root, "state"),
	}
	if _, err := o.SetSessionWorkspace(ctx, sessionID, project); err != nil {
		t.Fatal(err)
	}
	if got := o.actorCWD(sessionID); got != project {
		t.Fatalf("cwd = %q, want %q", got, project)
	}
}
