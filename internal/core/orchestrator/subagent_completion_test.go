package orchestrator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// scriptedBridge plays a fixed sequence of turns into the sub-agent loop. Each
// element of turns is the list of events emitted for that Complete() call.
type scriptedBridge struct {
	turns [][]bridge.StreamEvent
	call  int
}

func (b *scriptedBridge) ID() string              { return "scripted" }
func (b *scriptedBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *scriptedBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	idx := b.call
	b.call++
	out := make(chan bridge.StreamEvent, 8)
	go func() {
		defer close(out)
		if idx < len(b.turns) {
			for _, ev := range b.turns[idx] {
				out <- ev
			}
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

func toolCallEvent(name string, args map[string]any) bridge.StreamEvent {
	raw, _ := json.Marshal(args)
	return bridge.StreamEvent{
		Kind:     bridge.EventToolCall,
		ToolCall: &parse.ToolCall{Name: name, Arguments: raw},
	}
}

// TestTaskRunnerDoesNotCompleteOnToolLessTurn proves the premature-"done" bug is
// fixed: a turn where the model only narrates intent (no tool call) must NOT end
// the task. The loop nudges, and the model completes properly on a later turn.
func TestTaskRunnerDoesNotCompleteOnToolLessTurn(t *testing.T) {
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		// Turn 1: model only talks, calls no tool (the "kepentok" trigger).
		{{Kind: bridge.EventResponseDelta, Delta: "I'll switch to system_exec and build it."}},
		// Turn 2: after the nudge, it actually finishes via the terminal tool.
		{toolCallEvent("sapaloq_complete_task", map[string]any{"summary": "Website built."})},
	}}
	o := &Orchestrator{memoryDir: t.TempDir(), cfg: config.Config{}}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-1", Role: "task-runner", Status: "in_progress", Task: "build a site"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "done" {
		t.Fatalf("status = %q, want done (completed via terminal tool, not premature)", rec.Status)
	}
	if rec.Result != "Website built." {
		t.Fatalf("result = %q, want the complete_task summary", rec.Result)
	}
	if fake.call < 2 {
		t.Fatalf("expected at least 2 turns (nudge then complete), got %d", fake.call)
	}
}

// TestPlannerCompletesOnToolLessTurn confirms the nudge logic is scoped to
// executors: a planner that stops calling tools still finishes immediately
// (backward-compatible behavior).
func TestPlannerCompletesOnToolLessTurn(t *testing.T) {
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		{{Kind: bridge.EventResponseDelta, Delta: "Here is the plan."}},
	}}
	o := &Orchestrator{memoryDir: t.TempDir(), cfg: config.Config{}}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-2", Role: "planner", Status: "in_progress", Task: "plan it"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "done" {
		t.Fatalf("planner status = %q, want done", rec.Status)
	}
	if fake.call != 1 {
		t.Fatalf("planner should finish in 1 turn, got %d", fake.call)
	}
}

// TestRoleAllowsFallsBackOnUnknownAllowlist proves the config-mismatch guard:
// when a role's allowedTools name only tools the orchestrator does not
// implement, we ignore that (clearly wrong) list and fall back to the static
// policy instead of silently denying every real tool.
func TestRoleAllowsFallsBackOnUnknownAllowlist(t *testing.T) {
	o := &Orchestrator{cfg: config.Config{
		SubAgents: config.SubAgentsConfig{Roles: map[string]config.SubAgentRole{
			// The old broken live-config shape: abstract names, none real.
			"task-runner": {AllowedTools: []string{"gnome_*", "exec", "write_file", "mcp:*"}},
		}},
	}}
	// terminal_run is a real mutating tool that the bogus allowlist does not
	// match; the fallback must still grant it to task-runner.
	if !o.roleAllows("task-runner", "terminal_run") {
		t.Fatalf("task-runner should be allowed terminal_run via static fallback when allowlist names no real tool")
	}
	if !o.roleAllows("task-runner", "workspace_create_file") {
		t.Fatalf("task-runner should be allowed workspace_create_file via fallback")
	}
}

// TestRoleAllowsHonorsValidAllowlist confirms a correct (real-tool) allowlist is
// still authoritative — the guard only triggers when nothing matches.
func TestRoleAllowsHonorsValidAllowlist(t *testing.T) {
	o := &Orchestrator{cfg: config.Config{
		SubAgents: config.SubAgentsConfig{Roles: map[string]config.SubAgentRole{
			"scribe": {AllowedTools: []string{"workspace_read_file", "scribe_write_note"}},
		}},
	}}
	if !o.roleAllows("scribe", "workspace_read_file") {
		t.Fatalf("scribe should be allowed workspace_read_file")
	}
	if o.roleAllows("scribe", "terminal_run") {
		t.Fatalf("scribe must NOT be allowed terminal_run (not in its valid allowlist)")
	}
}

// TestPublishTaskUpdateFailureAlwaysSurfaces verifies the completion trigger:
// a failed task publishes an EventTaskUpdate even when notifyUserOnDone is off.
func TestPublishTaskUpdateFailureAlwaysSurfaces(t *testing.T) {
	b := bus.New()
	events, cancel := b.Subscribe(8)
	defer cancel()
	o := &Orchestrator{bus: b, cfg: config.Config{}}

	o.publishTaskUpdate("s1", taskRecord{ID: "t1", Role: "task-runner", Status: "failed", Error: "boom"})

	select {
	case ev := <-events:
		if ev.Data.Kind != bridge.EventTaskUpdate {
			t.Fatalf("kind = %q, want task_update", ev.Data.Kind)
		}
		if ev.Data.TaskStatus != "failed" {
			t.Fatalf("task_status = %q, want failed", ev.Data.TaskStatus)
		}
		if ev.Data.Summary == "" {
			t.Fatalf("summary should be populated")
		}
	default:
		t.Fatalf("expected a task_update event on the bus, got none")
	}
}

// TestPublishTaskUpdateDoneQuietByDefault verifies a successful task stays quiet
// unless notifyUserOnDone is enabled.
func TestPublishTaskUpdateDoneQuietByDefault(t *testing.T) {
	b := bus.New()
	events, cancel := b.Subscribe(8)
	defer cancel()
	o := &Orchestrator{bus: b, cfg: config.Config{}}

	o.publishTaskUpdate("s1", taskRecord{ID: "t1", Role: "task-runner", Status: "done", Result: "ok"})

	select {
	case ev := <-events:
		t.Fatalf("expected no event when notifyUserOnDone is false, got %q", ev.Data.Kind)
	default:
	}

	// With notify enabled, success surfaces.
	o.cfg.Orchestrator.Completion.NotifyUserOnDone = true
	o.publishTaskUpdate("s1", taskRecord{ID: "t1", Role: "task-runner", Status: "done", Result: "ok"})
	select {
	case ev := <-events:
		if ev.Data.TaskStatus != "done" {
			t.Fatalf("task_status = %q, want done", ev.Data.TaskStatus)
		}
	default:
		t.Fatalf("expected a done task_update when notifyUserOnDone is true")
	}
}
