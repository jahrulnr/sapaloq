package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestPerSessionWorkspaceIsolatedAcrossSwitchAndRestart(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	projectA := filepath.Join(root, "project-a")
	projectB := filepath.Join(root, "project-b")
	for _, dir := range []string{workspace, projectA, projectB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	open := func() *Orchestrator {
		store, err := chatstore.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		return &Orchestrator{
			cfg:          config.DefaultConfig(),
			chat:         store,
			entry:        config.LLMBridge{Key: "p", Model: "m"},
			workspaceDir: workspace,
			stateDir:     filepath.Join(root, "state"),
		}
	}
	ctx := context.Background()
	o := open()
	sessionA, err := o.ActiveSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.SetSessionWorkspace(ctx, sessionA, projectA); err != nil {
		t.Fatal(err)
	}
	sessionB, err := o.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := o.actorCWD(sessionB); got != workspace {
		t.Fatalf("new session should start at install default, got %q want %q", got, workspace)
	}
	if _, err := o.SetSessionWorkspace(ctx, sessionB, projectB); err != nil {
		t.Fatal(err)
	}
	if got, err := o.SwitchSession(ctx, sessionA); err != nil || got != sessionA {
		t.Fatalf("switch A: got=%q err=%v", got, err)
	}
	if st := o.RuntimeStatus(); st.SessionWorkspace != projectA {
		t.Fatalf("room A workspace = %q, want %q", st.SessionWorkspace, projectA)
	}
	if got, err := o.SwitchSession(ctx, sessionB); err != nil || got != sessionB {
		t.Fatalf("switch B: got=%q err=%v", got, err)
	}
	if st := o.RuntimeStatus(); st.SessionWorkspace != projectB {
		t.Fatalf("room B workspace = %q, want %q", st.SessionWorkspace, projectB)
	}

	restarted := open()
	if got := restarted.actorCWD(sessionA); got != projectA {
		t.Fatalf("after restart room A cwd = %q, want %q", got, projectA)
	}
	if got := restarted.actorCWD(sessionB); got != projectB {
		t.Fatalf("after restart room B cwd = %q, want %q", got, projectB)
	}
	restarted.chat = o.chat
	if _, err := restarted.SwitchSession(ctx, sessionA); err != nil {
		t.Fatal(err)
	}
	if st := restarted.RuntimeStatus(); st.SessionWorkspace != projectA {
		t.Fatalf("restarted active A workspace = %q, want %q", st.SessionWorkspace, projectA)
	}
}
