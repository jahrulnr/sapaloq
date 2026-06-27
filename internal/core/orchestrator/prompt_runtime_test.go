package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestRuntimeContextMessageUsesPersistedSessionWorkspace(t *testing.T) {
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
	msg := o.runtimeContextMessage(sessionID)
	if !strings.Contains(msg.Content, "workspace="+project) {
		t.Fatalf("runtime context missing persisted workspace %q:\n%s", project, msg.Content)
	}
	if strings.Contains(msg.Content, "workspace="+workspace) {
		t.Fatalf("runtime context should not use install default when session cwd is set:\n%s", msg.Content)
	}
}
