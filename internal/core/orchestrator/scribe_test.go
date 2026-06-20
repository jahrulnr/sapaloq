package orchestrator

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
)

func TestRoleAllowsFallbackPolicy(t *testing.T) {
	o := &Orchestrator{} // no SubAgents config → fallback policy
	// task-runner: full access.
	if !o.roleAllows("task-runner", "workspace_edit_file") {
		t.Fatalf("task-runner should be allowed to edit")
	}
	if !o.roleAllows("task-runner", "terminal_run") {
		t.Fatalf("task-runner should be allowed to run commands")
	}
	// planner (read-only): mutating tools denied, read tools allowed.
	if o.roleAllows("planner", "workspace_write_file") {
		t.Fatalf("planner must NOT be allowed to write")
	}
	if o.roleAllows("planner", "terminal_run") {
		t.Fatalf("planner must NOT be allowed to run commands")
	}
	if !o.roleAllows("planner", "workspace_read_file") {
		t.Fatalf("planner should be allowed to read")
	}
}

func TestRoleAllowsConfigAllowlistWithWildcard(t *testing.T) {
	o := &Orchestrator{cfg: config.Config{SubAgents: config.SubAgentsConfig{Roles: map[string]config.SubAgentRole{
		"scribe": {AllowedTools: []string{"workspace_read_file", "scribe_write_note", "sapaloq_*"}},
	}}}}
	if !o.roleAllows("scribe", "scribe_write_note") {
		t.Fatalf("scribe should be allowed scribe_write_note")
	}
	if !o.roleAllows("scribe", "sapaloq_complete_task") {
		t.Fatalf("wildcard sapaloq_* should match sapaloq_complete_task")
	}
	// Not in allowlist → denied, even though it's a mutating tool the fallback
	// would also deny; the point is the config list is authoritative.
	if o.roleAllows("scribe", "workspace_write_file") {
		t.Fatalf("scribe must NOT be allowed workspace_write_file")
	}
}

func TestToolsForRoleHonorsConfig(t *testing.T) {
	o := &Orchestrator{cfg: config.Config{SubAgents: config.SubAgentsConfig{Roles: map[string]config.SubAgentRole{
		"scribe": {AllowedTools: []string{"workspace_read_file", "scribe_write_note"}},
	}}}}
	got := o.toolsForRole("scribe")
	sort.Strings(got)
	want := []string{"scribe_write_note", "workspace_read_file"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("toolsForRole(scribe)=%v want %v", got, want)
	}
}

func TestToolsForRoleUnconfiguredUsesStatic(t *testing.T) {
	o := &Orchestrator{} // no config → static scribe profile
	got := o.toolsForRole("scribe")
	if !contains(got, "scribe_write_note") {
		t.Fatalf("static scribe profile should include scribe_write_note, got %v", got)
	}
}

func TestScribeWriteNoteAppendsToBoundaryPath(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "personal", "notes.md")
	o := &Orchestrator{cfg: config.Config{Storage: config.StorageConfig{
		Paths: []config.StoragePath{
			{ID: "personal-notes", Path: notes, Mode: "personal", Kind: "notes"},
		},
		Intents: map[string]string{"catat": "personal-notes"},
	}}}

	// By intent.
	res := o.toolScribeWriteNote(toolArgs{Note: "deploy runs on fridays", Intent: "catat"})
	if !strings.Contains(res, "appended") {
		t.Fatalf("expected append confirmation, got %q", res)
	}
	body, err := os.ReadFile(notes)
	if err != nil {
		t.Fatalf("read notes: %v", err)
	}
	if !strings.Contains(string(body), "deploy runs on fridays") {
		t.Fatalf("note not written, file=%q", string(body))
	}

	// By mode, second note appends (does not overwrite).
	o.toolScribeWriteNote(toolArgs{Note: "second note", Mode: "personal"})
	body, _ = os.ReadFile(notes)
	if !strings.Contains(string(body), "deploy runs on fridays") || !strings.Contains(string(body), "second note") {
		t.Fatalf("expected both notes appended, got %q", string(body))
	}
}

func TestScribeWriteNoteRejectsUnresolvable(t *testing.T) {
	o := &Orchestrator{cfg: config.Config{Storage: config.StorageConfig{
		Paths: []config.StoragePath{{ID: "p", Path: filepath.Join(t.TempDir(), "n.md"), Mode: "personal"}},
	}}}
	// No matching destination → error, nothing written.
	res := o.toolScribeWriteNote(toolArgs{Note: "x", Mode: "work"})
	if !strings.HasPrefix(res, "Error:") {
		t.Fatalf("expected error for unresolvable destination, got %q", res)
	}
	// Empty note → error.
	if res := o.toolScribeWriteNote(toolArgs{Mode: "personal"}); !strings.HasPrefix(res, "Error:") {
		t.Fatalf("expected error for empty note, got %q", res)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
