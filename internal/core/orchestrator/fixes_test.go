package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
	"github.com/jahrulnr/sapaloq/internal/vault"
)

// TestLatestPlanTaskIDRequiresPlanMd verifies fix #2: a planner task that only
// answered a question (no plan.md) must NOT be handed off as a plan, while a
// planner task that actually produced plan.md is selected.
func TestLatestPlanTaskIDRequiresPlanMd(t *testing.T) {
	o := &Orchestrator{memoryDir: t.TempDir()}

	// Planner A: answered only, no plan.md.
	a := taskRecord{ID: "task-a", SessionID: "s1", Role: "planner", Status: "done", Result: "just an answer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := o.writeTask(a); err != nil {
		t.Fatalf("write a: %v", err)
	}

	if got := o.latestPlanTaskID("s1"); got != "" {
		t.Fatalf("expected no plan task (no plan.md), got %q", got)
	}

	// Planner B: real plan with plan.md.
	b := taskRecord{ID: "task-b", SessionID: "s1", Role: "planner", Status: "done", Result: "plan", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC().Add(time.Second)}
	if err := o.writeTask(b); err != nil {
		t.Fatalf("write b: %v", err)
	}
	if err := os.WriteFile(filepath.Join(o.taskDir(b.ID), "plan.md"), []byte("## Goal\nx\n## Acceptance\n- [ ] y\n"), 0o600); err != nil {
		t.Fatalf("write plan.md: %v", err)
	}

	if got := o.latestPlanTaskID("s1"); got != "task-b" {
		t.Fatalf("expected task-b (has plan.md), got %q", got)
	}
}

// TestAuditToolWritesVault verifies fix #3: executed tool calls are appended to
// the vault audit log with reason "executed".
func TestAuditToolWritesVault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool-calls.jsonl")
	w, err := vault.New(path)
	if err != nil {
		t.Fatalf("vault.New: %v", err)
	}
	o := &Orchestrator{vault: w, entry: config.LLMBridge{Key: "tokenrouter"}}

	args, _ := json.Marshal(map[string]string{"path": "README.md"})
	o.auditTool("sess-1", "subagent:planner", parse.ToolCall{Name: "workspace_read_file", Arguments: args})

	entries, err := vault.ReadEntries(path, 10)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 vault entry, got %d", len(entries))
	}
	e := entries[0]
	if e.ResolvedName != "workspace_read_file" || e.Reason != "executed" || e.Source != "subagent:planner" || e.Provider != "tokenrouter" {
		t.Fatalf("unexpected entry: %+v", e)
	}

	// Nil vault writer must be a no-op (no panic).
	(&Orchestrator{}).auditTool("s", "ask", parse.ToolCall{Name: "x"})
}

// TestRoleMaxTurns verifies fix #4: per-role maxTurns is read from config with
// a safe fallback and clamping.
func TestRoleMaxTurns(t *testing.T) {
	o := &Orchestrator{}
	if got := o.roleMaxTurns("planner"); got != subAgentMaxTurns {
		t.Fatalf("fallback: got %d want %d", got, subAgentMaxTurns)
	}

	o.cfg.SubAgents = config.SubAgentsConfig{Roles: map[string]config.SubAgentRole{
		"planner":     {MaxTurns: 12},
		"task-runner": {MaxTurns: 999}, // clamped to 60
		"weird":       {MaxTurns: -5},  // invalid (<=0) → fallback to default
	}}
	if got := o.roleMaxTurns("planner"); got != 12 {
		t.Fatalf("planner: got %d want 12", got)
	}
	if got := o.roleMaxTurns("task-runner"); got != 60 {
		t.Fatalf("task-runner clamp: got %d want 60", got)
	}
	if got := o.roleMaxTurns("weird"); got != subAgentMaxTurns {
		t.Fatalf("weird invalid maxTurns should fall back: got %d want %d", got, subAgentMaxTurns)
	}
	if got := o.roleMaxTurns("missing"); got != subAgentMaxTurns {
		t.Fatalf("missing role fallback: got %d want %d", got, subAgentMaxTurns)
	}
}
