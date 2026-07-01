package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
	"github.com/jahrulnr/sapaloq/internal/vault"
)

func TestValidatePlanForAgentRequiresExplicitValidPlan(t *testing.T) {
	o := &Orchestrator{memoryDir: t.TempDir()}

	// Planner A: answered only, no plan.md.
	a := taskRecord{ID: "task-a", SessionID: "s1", Role: "planner", Status: "done", Result: "just an answer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := o.writeTask(a); err != nil {
		t.Fatalf("write a: %v", err)
	}

	if err := o.validatePlanForAgent("s1", "task-a"); err == nil || !strings.Contains(err.Error(), "no plan.md") {
		t.Fatalf("expected no-plan error, got %v", err)
	}

	// Planner B: real plan with plan.md.
	b := taskRecord{ID: "task-b", SessionID: "s1", Role: "planner", Status: "done", Result: "plan", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC().Add(time.Second)}
	if err := o.writeTask(b); err != nil {
		t.Fatalf("write b: %v", err)
	}
	if err := os.WriteFile(filepath.Join(o.taskDir(b.ID), "plan.md"), []byte("## Goal\nx\n## Acceptance\n- [ ] y\n"), 0o600); err != nil {
		t.Fatalf("write plan.md: %v", err)
	}

	if err := o.validatePlanForAgent("s1", "task-b"); err != nil {
		t.Fatalf("valid explicit plan rejected: %v", err)
	}
	if err := o.validatePlanForAgent("other-session", "task-b"); err == nil || !strings.Contains(err.Error(), "another session") {
		t.Fatalf("cross-session plan should be rejected, got %v", err)
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
	o.auditTool("sess-1", "subagent:planner", parse.ToolCall{Name: "read_file", Arguments: args})

	entries, err := vault.ReadEntries(path, 10)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 vault entry, got %d", len(entries))
	}
	e := entries[0]
	if e.ResolvedName != "read_file" || e.Reason != "executed" || e.Source != "subagent:planner" || e.Provider != "tokenrouter" {
		t.Fatalf("unexpected entry: %+v", e)
	}

	// Nil vault writer must be a no-op (no panic).
	(&Orchestrator{}).auditTool("s", "orchestrator", parse.ToolCall{Name: "x"})
}

// TestPlanWriteIsIterable verifies write_plan is non-terminal
// (planner can revise) and that the planner can read its own plan back.
func TestPlanWriteIsIterable(t *testing.T) {
	o := &Orchestrator{memoryDir: t.TempDir()}
	rec := &taskRecord{ID: "task-plan-1", Role: "planner", Status: "in_progress"}
	if err := o.writeTask(*rec); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var result strings.Builder

	first, _ := json.Marshal(map[string]string{"markdown": "## Goal\nv1\n"})
	res := o.runBackgroundTool(nil, rec, &result, parse.ToolCall{Name: "write_plan", Arguments: first}, parseToolArgs(first), nil)
	if res.stop {
		t.Fatalf("write_plan_markdown must NOT be terminal (planner needs to iterate)")
	}
	if !strings.Contains(res.text, "plan.md") {
		t.Fatalf("expected path hint in response, got: %s", res.text)
	}

	// Planner reads its own plan back.
	readRes := o.runBackgroundTool(nil, rec, &result, parse.ToolCall{Name: "read_plan", Arguments: []byte(`{}`)}, toolArgs{}, nil)
	if !strings.Contains(readRes.text, "v1") {
		t.Fatalf("planner should read its own plan, got: %s", readRes.text)
	}

	// Revise.
	second, _ := json.Marshal(map[string]string{"markdown": "## Goal\nv2 revised\n"})
	o.runBackgroundTool(nil, rec, &result, parse.ToolCall{Name: "write_plan", Arguments: second}, parseToolArgs(second), nil)
	if got := o.readPlanMarkdown(rec.ID); !strings.Contains(got, "v2 revised") {
		t.Fatalf("revision not persisted, got: %q", got)
	}
}

// TestRoleMaxTurns verifies the per-role tool-loop budget policy:
//   - the executor (task-runner) with no config runs UNLIMITED (sentinel < 0),
//     so a productive long task is never force-failed on an arbitrary turn
//     count; the no-progress / identical-tool / wall-time / tool-call guards are
//     the real stoppers.
//   - short-lived roles (planner/scribe) with no config inherit the chat budget
//     (Continuation.MaxInferenceTurns, default 128).
//   - an explicit per-role maxTurns always wins, honored as-is with no clamp.
func TestRoleMaxTurns(t *testing.T) {
	o := &Orchestrator{}
	defaultTurns := o.cfg.Orchestrator.WithDefaults().Continuation.MaxInferenceTurns
	if defaultTurns != 128 {
		t.Fatalf("precondition: default MaxInferenceTurns got %d want 128", defaultTurns)
	}
	// Executor with no config → unlimited (negative sentinel).
	if got := o.roleMaxTurns("task-runner"); got >= 0 {
		t.Fatalf("task-runner default should be unlimited (<0): got %d", got)
	}
	// Short-lived roles inherit the chat budget.
	if got := o.roleMaxTurns("planner"); got != defaultTurns {
		t.Fatalf("planner fallback should inherit chat budget: got %d want %d", got, defaultTurns)
	}
	if got := o.roleMaxTurns("scribe"); got != defaultTurns {
		t.Fatalf("scribe fallback should inherit chat budget: got %d want %d", got, defaultTurns)
	}

	o.cfg.SubAgents = config.SubAgentsConfig{Roles: map[string]config.SubAgentRole{
		"planner":     {MaxTurns: 12},
		"task-runner": {MaxTurns: 999}, // explicit cap wins, honored as-is
	}}
	if got := o.roleMaxTurns("planner"); got != 12 {
		t.Fatalf("planner: got %d want 12", got)
	}
	if got := o.roleMaxTurns("task-runner"); got != 999 {
		t.Fatalf("task-runner explicit cap should be honored: got %d want 999", got)
	}
	// A role with no entry still follows its default (executor → unlimited).
	if got := o.roleMaxTurns("task-runner-2"); got != defaultTurns {
		t.Fatalf("unknown role fallback: got %d want %d", got, defaultTurns)
	}
}
