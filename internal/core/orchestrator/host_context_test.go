package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/hostcontext"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestHostContextBlockInjectedInForegroundMessages(t *testing.T) {
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
	raw, _ := json.Marshal(hostcontext.Snapshot{
		Version: hostcontext.Version,
		Workspace: hostcontext.Workspace{
			SessionWorkspace: project,
		},
		Attachments: []hostcontext.Attachment{
			{Path: filepath.Join(project, "main.go"), Kind: "file", Name: "main.go"},
		},
		UI: hostcontext.UI{Mode: "orchestrator", ComposeAttachmentCount: 1},
	})
	o.setSessionHostContext(sessionID, raw)

	cwdBefore := o.actorCWD(sessionID)
	msgs, err := o.buildForegroundActorMessages(ctx, sessionID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if cwdAfter := o.actorCWD(sessionID); cwdAfter != cwdBefore {
		t.Fatalf("host context must not change actorCWD: before %q after %q", cwdBefore, cwdAfter)
	}
	found := false
	for _, m := range msgs {
		if m.Role != "system" {
			continue
		}
		if strings.Contains(m.Content, "# Host context (ephemeral)") {
			found = true
			if !strings.Contains(m.Content, "attachment_paths=") {
				t.Fatalf("missing attachment_paths in host block:\n%s", m.Content)
			}
			break
		}
	}
	if !found {
		t.Fatal("host context system block not found in foreground messages")
	}
}

func TestHostContextAbsentDegraded(t *testing.T) {
	o := &Orchestrator{cfg: config.DefaultConfig()}
	if block := o.hostContextBlock("chat-1"); block != "" {
		t.Fatalf("expected empty block, got %q", block)
	}
}

func TestEstimateOverheadIncludesHostBlock(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
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
	without := o.estimatePerTurnOverhead(ctx, sessionID, "hello")
	raw, _ := json.Marshal(hostcontext.Snapshot{
		Version: hostcontext.Version,
		Workspace: hostcontext.Workspace{
			SessionWorkspace: workspace,
		},
	})
	o.setSessionHostContext(sessionID, raw)
	with := o.estimatePerTurnOverhead(ctx, sessionID, "hello")
	block := o.hostContextBlock(sessionID)
	wantDelta := estimateTextTokens(block)
	if with <= without {
		t.Fatalf("overhead with host context should increase: without=%d with=%d blockTokens=%d", without, with, wantDelta)
	}
	if delta := with - without; delta != wantDelta {
		t.Fatalf("overhead delta=%d want host block tokens=%d", delta, wantDelta)
	}
}

func TestSessionDeleteClearsHostContext(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
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
	raw, _ := json.Marshal(hostcontext.Snapshot{
		Version: hostcontext.Version,
		Workspace: hostcontext.Workspace{SessionWorkspace: workspace},
	})
	o.setSessionHostContext(sessionID, raw)
	if o.hostContextBlock(sessionID) == "" {
		t.Fatal("expected host block before delete")
	}
	if _, _, err := o.DeleteSession(ctx, sessionID); err != nil {
		t.Fatal(err)
	}
	if o.hostContextBlock(sessionID) != "" {
		t.Fatal("host context should be cleared after session delete")
	}
}
