package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestSystemExecRunsAnywhere(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Run with an explicit cwd outside the workspace root.
	got := toolSystemExec(context.Background(), toolArgs{Command: "ls", Cwd: dir})
	if !strings.Contains(got, "marker") {
		t.Fatalf("expected `ls` to list marker in cwd, got %q", got)
	}
}

func TestSystemExecReadsHostFile(t *testing.T) {
	// system_exec is the single host tool; it must be able to read any host file
	// (the role system_read_file used to play) via standard utilities.
	dir := t.TempDir()
	p := filepath.Join(dir, "hosts.txt")
	if err := os.WriteFile(p, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := toolSystemExec(context.Background(), toolArgs{Command: "cat " + p})
	if !strings.Contains(got, "127.0.0.1 localhost") {
		t.Fatalf("expected file content via cat, got %q", got)
	}
}

func TestSystemExecEmptyCommand(t *testing.T) {
	if got := toolSystemExec(context.Background(), toolArgs{}); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("expected error for empty command, got %q", got)
	}
}

func TestSharedToolDispatchesSystemExec(t *testing.T) {
	// system_exec must be handled by the shared dispatcher (available in every
	// mode); system_read_file no longer exists.
	text, handled := runSharedTool(context.Background(), parse.ToolCall{Name: "system_exec", Arguments: []byte(`{"command":"echo ok"}`)})
	if !handled || !strings.Contains(text, "ok") {
		t.Fatalf("shared dispatch failed for system_exec: handled=%v text=%q", handled, text)
	}
	if _, handled := runSharedTool(context.Background(), parse.ToolCall{Name: "system_read_file", Arguments: []byte(`{"path":"/etc/hosts"}`)}); handled {
		t.Fatalf("system_read_file should no longer be dispatched")
	}
}

func TestSystemExecInAllModeProfiles(t *testing.T) {
	for mode, profile := range map[string][]string{"ask": askTools, "plan": planTools, "agent": agentTools} {
		if !containsTool(profile, "system_exec") {
			t.Fatalf("%s profile missing system_exec: %v", mode, profile)
		}
		if containsTool(profile, "system_read_file") {
			t.Fatalf("%s profile still contains removed system_read_file: %v", mode, profile)
		}
	}
}

func containsTool(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}
