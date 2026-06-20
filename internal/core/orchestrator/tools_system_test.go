package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestSystemReadFileReadsOutsideWorkspace(t *testing.T) {
	// Write a file in a temp dir that is NOT the workspace root, then read it
	// by absolute path — workspace_read_file would reject this, system_read_file
	// must allow it.
	dir := t.TempDir()
	p := filepath.Join(dir, "hosts.txt")
	if err := os.WriteFile(p, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := toolSystemReadFile(toolArgs{Path: p})
	if !strings.Contains(got, "127.0.0.1 localhost") {
		t.Fatalf("expected file content, got %q", got)
	}
}

func TestSystemReadFileMissingPath(t *testing.T) {
	if got := toolSystemReadFile(toolArgs{}); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("expected error for empty path, got %q", got)
	}
}

func TestSystemReadFileRejectsBinary(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bin")
	if err := os.WriteFile(p, []byte{0x00, 0x01, 0x02, 0x00, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	got := toolSystemReadFile(toolArgs{Path: p})
	if !strings.Contains(got, "binary") {
		t.Fatalf("expected binary refusal, got %q", got)
	}
}

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

func TestSystemExecEmptyCommand(t *testing.T) {
	if got := toolSystemExec(context.Background(), toolArgs{}); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("expected error for empty command, got %q", got)
	}
}

func TestSharedToolDispatchesSystemTools(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(p, []byte("hello\n"), 0o644)

	// system_read_file must be handled by the shared dispatcher (available in
	// every mode).
	text, handled := runSharedTool(context.Background(), parse.ToolCall{Name: "system_read_file", Arguments: []byte(`{"path":"` + p + `"}`)})
	if !handled || !strings.Contains(text, "hello") {
		t.Fatalf("shared dispatch failed for system_read_file: handled=%v text=%q", handled, text)
	}
	text, handled = runSharedTool(context.Background(), parse.ToolCall{Name: "system_exec", Arguments: []byte(`{"command":"echo ok"}`)})
	if !handled || !strings.Contains(text, "ok") {
		t.Fatalf("shared dispatch failed for system_exec: handled=%v text=%q", handled, text)
	}
}

func TestSystemToolsInAllModeProfiles(t *testing.T) {
	for _, profile := range map[string][]string{"ask": askTools, "plan": planTools, "agent": agentTools} {
		if !containsTool(profile, "system_exec") || !containsTool(profile, "system_read_file") {
			t.Fatalf("profile missing unrestricted system tools: %v", profile)
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
