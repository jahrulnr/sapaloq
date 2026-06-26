package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		{{Kind: bridge.EventResponseDelta, Delta: "I'll switch to exec and build it."}},
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

func TestTaskRunnerFailsWhenItNeverSignalsTerminalState(t *testing.T) {
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		{{Kind: bridge.EventResponseDelta, Delta: "I will do it."}},
		{{Kind: bridge.EventResponseDelta, Delta: "Let me invoke the tool."}},
		{{Kind: bridge.EventResponseDelta, Delta: "I am about to invoke it."}},
	}}
	o := &Orchestrator{memoryDir: t.TempDir(), cfg: config.Config{}}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-stuck", Role: "task-runner", Status: "in_progress", Task: "build a site"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	if rec.Status != "failed" {
		t.Fatalf("status = %q, want failed", rec.Status)
	}
	if rec.Error == "" {
		t.Fatalf("stuck executor must record a concrete failure")
	}
}

func TestSubAgentProgressDoesNotPersistBridgeDoneAsTaskCompletion(t *testing.T) {
	progressDir := t.TempDir()
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		{toolCallEvent("sapaloq_complete_task", map[string]any{"summary": "done for real"})},
	}}
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		progress:  newAsyncProgressWriter(ProgressWriter{Dir: progressDir}),
		cfg:       config.Config{},
	}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := &taskRecord{ID: "task-progress", Role: "task-runner", Status: "in_progress", Task: "finish"}

	o.runSubAgentLoop(context.Background(), snap, "s1", rec)

	raw, err := os.ReadFile(filepath.Join(progressDir, "orch-task-progress.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"kind":"done"`)) {
		t.Fatalf("bridge turn completion leaked into task progress: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"kind":"task_update"`)) {
		t.Fatalf("tool activity task_update missing from progress: %s", raw)
	}
}

// TestPlannerFinishesCleanlyWithoutTerminalTool confirms that, under the
// unified "stop only via terminal tool" model, a non-executor role (planner)
// that answers and then goes quiet WITHOUT calling a terminal tool still ends
// as `done` (not `failed`). Unlike the old behavior it no longer finishes in a
// single tool-less turn - the run continues until the no-progress finish closes
// it cleanly - but the OUTCOME for a planner is still a clean completion. Only
// an executor (task-runner) that never signals a terminal tool is treated as a
// failure (see TestTaskRunnerFailsWhenItNeverSignalsTerminalState).
func TestPlannerFinishesCleanlyWithoutTerminalTool(t *testing.T) {
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
	// The run is bounded by the no-progress finish, so it takes more than one
	// turn now; it must still be bounded (not run away).
	if fake.call < 2 {
		t.Fatalf("planner run should continue past the first tool-less turn, got %d", fake.call)
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
			"task-runner": {AllowedTools: []string{"gnome_*", "mcp:*", "emit_progress", "ask_orchestrator"}},
		}},
	}}
	// exec is a real tool the bogus allowlist does not match; the fallback must
	// still grant it to task-runner.
	if !o.roleAllows("task-runner", "exec") {
		t.Fatalf("task-runner should be allowed exec via static fallback when allowlist names no real tool")
	}
	if !o.roleAllows("task-runner", "create_file") {
		t.Fatalf("task-runner should be allowed create_file via fallback")
	}
}

// TestRoleAllowsHonorsValidAllowlist confirms a correct (real-tool) allowlist is
// still authoritative - the guard only triggers when nothing matches.
func TestRoleAllowsHonorsValidAllowlist(t *testing.T) {
	o := &Orchestrator{cfg: config.Config{
		SubAgents: config.SubAgentsConfig{Roles: map[string]config.SubAgentRole{
			"scribe": {AllowedTools: []string{"read_file", "scribe_write_note"}},
		}},
	}}
	if !o.roleAllows("scribe", "read_file") {
		t.Fatalf("scribe should be allowed read_file")
	}
	if o.roleAllows("scribe", "exec") {
		t.Fatalf("scribe must NOT be allowed exec (not in its valid allowlist)")
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

// TestPublishTaskUpdateDoneAlwaysSurfaces verifies chat certainty is not tied
// to the optional desktop-notification preference.
func TestPublishTaskUpdateDoneAlwaysSurfaces(t *testing.T) {
	b := bus.New()
	events, cancel := b.Subscribe(8)
	defer cancel()
	dir := t.TempDir()
	o := &Orchestrator{bus: b, cfg: config.Config{}, progress:  newAsyncProgressWriter(ProgressWriter{Dir: dir})}

	o.publishTaskUpdate("s1", taskRecord{ID: "t1", Role: "task-runner", Status: "done", Result: "ok"})

	select {
	case ev := <-events:
		if ev.Data.TaskStatus != "done" {
			t.Fatalf("task_status = %q, want done", ev.Data.TaskStatus)
		}
	default:
		t.Fatalf("expected a done task_update regardless of notifyUserOnDone")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "orch-t1.jsonl"))
	if err != nil {
		t.Fatalf("terminal task event not persisted to progress: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"kind":"task_update"`)) {
		t.Fatalf("progress missing task_update: %s", raw)
	}
}

func TestRecentTaskUpdatesRehydratesLiveStateOnly(t *testing.T) {
	now := time.Now().UTC()
	o := &Orchestrator{memoryDir: t.TempDir()}
	// t1 is live (in_progress); t2 is terminal (failed) and must NOT be
	// rehydrated - its outcome is already persisted as an assistant chat bubble
	// via speakTaskCompletion, and re-emitting it made the chat room fill with a
	// verbose status timeline on every widget open.
	for _, record := range []taskRecord{
		{ID: "t1", SessionID: "s1", Role: "task-runner", Status: "in_progress", CreatedAt: now, UpdatedAt: now},
		{ID: "t2", SessionID: "s1", Role: "task-runner", Status: "failed", Error: "boom", CreatedAt: now, UpdatedAt: now.Add(time.Second)},
		{ID: "t3", SessionID: "s1", Role: "planner", Status: "done", Result: "ok", CreatedAt: now, UpdatedAt: now.Add(2 * time.Second)},
		{ID: "t4", SessionID: "s1", Role: "task-runner", Status: "awaiting_clarification", Question: "which?", CreatedAt: now, UpdatedAt: now.Add(3 * time.Second)},
	} {
		if err := o.writeTask(record); err != nil {
			t.Fatal(err)
		}
	}
	events := o.RecentTaskUpdates(20)
	// Only the live (in_progress) and paused (awaiting_clarification) tasks
	// rehydrate; terminal done/failed are dropped.
	if len(events) != 2 {
		t.Fatalf("got %d updates, want 2 (live + paused only): %+v", len(events), events)
	}
	ids := []string{events[0].TaskID, events[1].TaskID}
	if ids[0] != "t1" || ids[1] != "t4" {
		t.Fatalf("unexpected rehydrated tasks: %+v", ids)
	}
	if events[0].TaskStatus != "in_progress" {
		t.Fatalf("t1 status = %q, want in_progress", events[0].TaskStatus)
	}
	if events[1].TaskStatus != "awaiting_clarification" || events[1].Summary == "" {
		t.Fatalf("paused snapshot incomplete: %+v", events[1])
	}
}

func TestRecoverOrphanedTasksFailsDetachedWorkers(t *testing.T) {
	now := time.Now().UTC()
	b := bus.New()
	events, cancel := b.Subscribe(8)
	defer cancel()
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		progress:  newAsyncProgressWriter(ProgressWriter{Dir: t.TempDir()}),
		bus:       b,
	}
	for _, record := range []taskRecord{
		{ID: "pending", SessionID: "s1", Role: "task-runner", Status: "pending", CreatedAt: now, UpdatedAt: now},
		{ID: "working", SessionID: "s1", Role: "task-runner", Status: "in_progress", CreatedAt: now, UpdatedAt: now},
		{ID: "complete", SessionID: "s1", Role: "task-runner", Status: "done", Result: "ok", CreatedAt: now, UpdatedAt: now},
	} {
		if err := o.writeTask(record); err != nil {
			t.Fatal(err)
		}
	}

	o.recoverOrphanedTasks()

	for _, id := range []string{"pending", "working"} {
		record, err := o.readTask(id)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status != "failed" || !strings.Contains(record.Error, "orphaned") {
			t.Fatalf("%s not recovered as explicit failure: %+v", id, record)
		}
	}
	complete, err := o.readTask("complete")
	if err != nil {
		t.Fatal(err)
	}
	if complete.Status != "done" {
		t.Fatalf("terminal task changed during recovery: %+v", complete)
	}

	seen := 0
	for {
		select {
		case ev := <-events:
			if ev.Data.Kind == bridge.EventTaskUpdate && ev.Data.TaskStatus == "failed" {
				seen++
			}
		default:
			if seen != 2 {
				t.Fatalf("published %d orphan recovery updates, want 2", seen)
			}
			return
		}
	}
}
