package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestExecRunsAnywhere(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Run with an explicit cwd anywhere on the host.
	got := toolExec(context.Background(), toolArgs{Command: "ls", Cwd: dir})
	if !strings.Contains(got, "marker") {
		t.Fatalf("expected `ls` to list marker in cwd, got %q", got)
	}
}

func TestExecReadsHostFile(t *testing.T) {
	// exec is the single host command tool; it must be able to read any host
	// file via standard utilities.
	dir := t.TempDir()
	p := filepath.Join(dir, "hosts.txt")
	if err := os.WriteFile(p, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := toolExec(context.Background(), toolArgs{Command: "cat " + p})
	if !strings.Contains(got, "127.0.0.1 localhost") {
		t.Fatalf("expected file content via cat, got %q", got)
	}
}

func TestExecEmptyCommand(t *testing.T) {
	if got := toolExec(context.Background(), toolArgs{}); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("expected error for empty command, got %q", got)
	}
}

// TestFileToolsAreNotWorkspaceBound proves the bug fix: file tools accept any
// host path (absolute, outside the process CWD) without a "outside workspace
// root" rejection. SapaLOQ is unrestricted by design.
func TestFileToolsAreNotWorkspaceBound(t *testing.T) {
	dir := t.TempDir() // an absolute path outside the project CWD
	p := filepath.Join(dir, "out.txt")

	if got := toolWriteFile(toolArgs{Path: p, Content: "hello"}, false); strings.HasPrefix(got, "Error:") {
		t.Fatalf("write_file rejected an out-of-CWD absolute path: %q", got)
	}
	if got := toolReadFile(toolArgs{Path: p}); !strings.Contains(got, "hello") {
		t.Fatalf("read_file failed for out-of-CWD absolute path: %q", got)
	}
	if got := toolEditFile(toolArgs{Path: p, OldString: "hello", NewString: "world"}); strings.HasPrefix(got, "Error:") {
		t.Fatalf("edit_file rejected an out-of-CWD absolute path: %q", got)
	}
	if got := toolReadFile(toolArgs{Path: p}); !strings.Contains(got, "world") {
		t.Fatalf("edit_file did not apply: %q", got)
	}
	if got := toolListDir(toolArgs{Path: dir}); !strings.Contains(got, "out.txt") {
		t.Fatalf("list_dir failed for out-of-CWD absolute path: %q", got)
	}
	if got := toolDeleteFile(toolArgs{Path: p}); strings.HasPrefix(got, "Error:") {
		t.Fatalf("delete_file rejected an out-of-CWD absolute path: %q", got)
	}
}

func TestSharedToolDispatchesExec(t *testing.T) {
	// exec must be handled by the shared dispatcher (available in every mode).
	text, handled := runSharedTool(context.Background(), parse.ToolCall{Name: "exec", Arguments: []byte(`{"command":"echo ok"}`)})
	if !handled || !strings.Contains(text, "ok") {
		t.Fatalf("shared dispatch failed for exec: handled=%v text=%q", handled, text)
	}
	if _, handled := runSharedTool(context.Background(), parse.ToolCall{Name: "system_exec", Arguments: []byte(`{"command":"echo ok"}`)}); handled {
		t.Fatalf("legacy system_exec name should no longer be dispatched")
	}
}

func TestExecInAllModeProfiles(t *testing.T) {
	for mode, profile := range map[string][]string{"ask": askTools, "plan": planTools, "agent": agentTools} {
		if !containsTool(profile, "exec") {
			t.Fatalf("%s profile missing exec: %v", mode, profile)
		}
		if containsTool(profile, "system_exec") || containsTool(profile, "terminal_run") {
			t.Fatalf("%s profile still contains a legacy exec name: %v", mode, profile)
		}
	}
}

func TestSubAgentSharedToolStillHonorsRolePolicy(t *testing.T) {
	o := &Orchestrator{cfg: config.Config{SubAgents: config.SubAgentsConfig{Roles: map[string]config.SubAgentRole{
		"planner": {AllowedTools: []string{"exec"}},
		"scribe":  {AllowedTools: []string{"scribe_write_note"}},
	}}}}
	call := parse.ToolCall{Name: "exec", Arguments: []byte(`{"command":"printf allowed"}`)}

	planner := &taskRecord{ID: "plan", Role: "planner"}
	got := o.handleSubAgentTool(context.Background(), planner, &strings.Builder{}, call)
	if !strings.Contains(got.text, "allowed") {
		t.Fatalf("planner exec should run for exploration, got %q", got.text)
	}

	scribe := &taskRecord{ID: "scribe", Role: "scribe"}
	got = o.handleSubAgentTool(context.Background(), scribe, &strings.Builder{}, call)
	if !strings.Contains(got.text, "not allowed for role scribe") {
		t.Fatalf("scribe poisoned exec should be denied, got %q", got.text)
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
