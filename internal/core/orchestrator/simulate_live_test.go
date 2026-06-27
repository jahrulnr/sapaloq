package orchestrator

// simulate_live_test.go runs the orchestrator/planner/agent loop against a REAL
// LLM (Blackbox, an OpenAI-compatible provider) in exactly ONE role per test
// while every OTHER role - and the tooling that would otherwise hit the network
// or a real sub-agent - is mocked. This proves the live model behaves at each
// hand-off boundary without spending tokens on the whole tree.
//
// Why this exists: a context-sensitive model (e.g. MiniMax) sometimes narrates
// a delegation ("oke aku delegasikan ke agent") and ends its turn WITHOUT
// emitting the spawn tool call. The ask.md "spawn-before-acknowledge" fix is
// meant to stop that; mode 1 below is the live regression for it.
//
// These tests are GATED: without SAPALOQ_BLACKBOX_E2E=1 and a working token in
// the configured env var they t.Skip, so `go test ./...` stays green offline.
// They live in package orchestrator (not e2e) so they can drive the internal
// runTaskActor / providerSnapshot / spawnBackground seams directly.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/provider"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// ---------------------------------------------------------------------------
// Live-credential gate
// ---------------------------------------------------------------------------

// blackboxEnabled reports whether the live Blackbox simulate suite is opted in.
func blackboxEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SAPALOQ_BLACKBOX_E2E")))
	return v == "1" || v == "true" || v == "yes"
}

// blackboxEntry builds the provider entry from env, with sane Blackbox defaults
// so the test reads no secrets from source. Override any field via env.
func blackboxEntry() config.LLMBridge {
	endpoint := strings.TrimSpace(os.Getenv("BLACKBOX_ENDPOINT"))
	if endpoint == "" {
		endpoint = "https://api.blackbox.ai/v1/chat/completions"
	}
	// The provider bridge POSTs to the endpoint verbatim (no path is appended),
	// so accept a bare base URL (…/v1) and complete it to the OpenAI route.
	if strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/v1") {
		endpoint = strings.TrimRight(endpoint, "/") + "/chat/completions"
	}
	model := strings.TrimSpace(os.Getenv("BLACKBOX_MODEL"))
	if model == "" {
		model = "blackboxai/openai/gpt-4o"
	}
	credEnv := strings.TrimSpace(os.Getenv("BLACKBOX_CREDENTIALS_ENV"))
	if credEnv == "" {
		credEnv = "BLACKBOX_API_KEY"
	}
	return config.LLMBridge{
		Key:            "blackbox",
		Driver:         "provider-bridge",
		Endpoint:       endpoint,
		Model:          model,
		CredentialsEnv: credEnv,
		Parser:         "openai",
	}
}

// requireBlackbox skips unless the live suite is enabled AND a token is present
// in the configured env var, then returns a ready real provider bridge + entry.
func requireBlackbox(t *testing.T) (bridge.Bridge, config.LLMBridge) {
	t.Helper()
	if !blackboxEnabled() {
		t.Skip("set SAPALOQ_BLACKBOX_E2E=1 (+ BLACKBOX_API_KEY) to run the live Blackbox simulate suite")
	}
	entry := blackboxEntry()
	if strings.TrimSpace(os.Getenv(entry.CredentialsEnv)) == "" {
		t.Skipf("live Blackbox token env %s is empty - skipping", entry.CredentialsEnv)
	}
	br, err := provider.New(entry)
	if err != nil {
		t.Fatalf("build Blackbox provider bridge: %v", err)
	}
	if !br.Caps().LiveAPI {
		t.Skipf("Blackbox bridge reports no LiveAPI (token env %s) - skipping", entry.CredentialsEnv)
	}
	return br, entry
}

// ---------------------------------------------------------------------------
// Role-routing bridge: real LLM for the role under test, scripted mock for the
// rest. Role is detected from the system prompt the orchestrator assembles.
// ---------------------------------------------------------------------------

type simRole int

const (
	roleUnknown simRole = iota
	roleAsk
	rolePlanner
	roleAgent
)

// detectRole inspects the assembled system messages for the stable role markers
// that systemPrompt() bakes in from ask.md / planner.md / agent.md, falling back
// to declared tools (only Ask offers sapaloq_spawn_*).
func detectRole(req bridge.Request) simRole {
	var sys strings.Builder
	for _, m := range req.Messages {
		if m.Role == "system" {
			sys.WriteString(m.Content)
			sys.WriteString("\n")
		}
	}
	s := sys.String()
	switch {
	case strings.Contains(s, "Ask orchestrator"):
		return roleAsk
	case strings.Contains(s, "planner (Plan mode)"):
		return rolePlanner
	case strings.Contains(s, "executor (Agent mode)"):
		return roleAgent
	}
	for _, tn := range req.DeclaredTools {
		if tn == "sapaloq_spawn_agent" || tn == "sapaloq_spawn_plan" {
			return roleAsk
		}
	}
	return roleUnknown
}

// scriptFn produces the events a mocked role emits for a given Complete() call.
// callIndex is how many times THIS role has already been invoked (0-based), so a
// script can answer differently on the first turn vs. after a tool result.
type scriptFn func(callIndex int, req bridge.Request) []bridge.StreamEvent

// roleRoutingBridge sends the role under test to a real bridge and every other
// role to its scripted mock. It records, per role, how many times it was called
// so the tests can assert the live model actually reached each hand-off.
type roleRoutingBridge struct {
	real   bridge.Bridge
	under  simRole
	script map[simRole]scriptFn

	mu    sync.Mutex
	calls map[simRole]int
}

func newRoleRouter(real bridge.Bridge, under simRole) *roleRoutingBridge {
	return &roleRoutingBridge{
		real:   real,
		under:  under,
		script: map[simRole]scriptFn{},
		calls:  map[simRole]int{},
	}
}

func (b *roleRoutingBridge) ID() string { return "role-router" }
func (b *roleRoutingBridge) Caps() bridge.BridgeCaps {
	return bridge.BridgeCaps{Tools: true, LiveAPI: true}
}

func (b *roleRoutingBridge) callCount(r simRole) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls[r]
}

func (b *roleRoutingBridge) Complete(ctx context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	role := detectRole(req)
	b.mu.Lock()
	idx := b.calls[role]
	b.calls[role]++
	b.mu.Unlock()

	if role == b.under {
		return b.real.Complete(ctx, req)
	}
	fn := b.script[role]
	out := make(chan bridge.StreamEvent, 16)
	go func() {
		defer close(out)
		if fn != nil {
			for _, ev := range fn(idx, req) {
				select {
				case <-ctx.Done():
					return
				case out <- ev:
				}
			}
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// toolEvent builds an EventToolCall with JSON arguments (helper local to the
// simulate suite so it doesn't depend on the other test files' helpers).
func simToolEvent(name string, args map[string]any) bridge.StreamEvent {
	raw, _ := json.Marshal(args)
	return bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &parse.ToolCall{Name: name, Arguments: raw}}
}

func simTextEvent(s string) bridge.StreamEvent {
	return bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: s}
}

// newSimOrchestrator builds a minimal real Orchestrator backed by the router,
// with a temp memory/tasks dir so spawnBackground + readTask work end to end.
func newSimOrchestrator(t *testing.T, router *roleRoutingBridge, entry config.LLMBridge) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	// Pin ALL runtime storage (memory/tasks/progress/vault/…) under a temp dir
	// so the suite never touches the real ~/SapaLOQ, and point the active
	// provider at the live Blackbox entry.
	cfg.Runtime.DataDir = dir
	cfg.LLMBridge.ProviderKey = entry.Key
	cfg.LLMBridge.Providers = []config.LLMBridge{entry}
	if err := config.EnsureRuntimeDirs(config.RuntimeDirs(cfg)); err != nil {
		t.Fatalf("ensure runtime dirs: %v", err)
	}
	o, err := New(cfg, "", router, bus.New())
	if err != nil {
		t.Fatalf("orchestrator.New: %v", err)
	}
	// Pin the live entry + router so snapshot() hands the real model
	// coordinates to both the chat loop and any spawned sub-agent.
	o.entry = entry
	o.bridge = router
	return o
}

// ---------------------------------------------------------------------------
// Mode 1: live ORCHESTRATOR (Ask) ↔ mocked planner + mocked agent round-trip.
// Proves the live model emits the spawn tool call at each hand-off rather than
// narrating it and ending the turn (the ask.md regression).
// ---------------------------------------------------------------------------

func TestSimulateOrchestratorPlannerAgentRoundTrip(t *testing.T) {
	real, entry := requireBlackbox(t)
	router := newRoleRouter(real, roleAsk)
	// Mocked planner: immediately writes a trivial plan and finishes.
	router.script[rolePlanner] = func(_ int, _ bridge.Request) []bridge.StreamEvent {
		return []bridge.StreamEvent{
			simToolEvent("write_plan", map[string]any{
				"markdown": "## Goal\nCreate hello.txt\n\n## Acceptance\n- [ ] hello.txt exists",
			}),
			simToolEvent("sapaloq_complete_task", map[string]any{"summary": "Plan ready."}),
		}
	}
	// Mocked agent: completes the (mocked) work.
	router.script[roleAgent] = func(_ int, _ bridge.Request) []bridge.StreamEvent {
		return []bridge.StreamEvent{
			simToolEvent("sapaloq_complete_task", map[string]any{"summary": "hello.txt created."}),
		}
	}

	o := newSimOrchestrator(t, router, entry)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Turn 1: ask the orchestrator to plan a task. We expect it to spawn the
	// planner (a real tool call), not merely talk about it.
	drainChat(t, o, ctx, "sim-1", "Tolong bikinkan rencana untuk membuat file hello.txt, lalu delegasikan pembuatannya.")

	waitFor(t, 30*time.Second, func() bool { return router.callCount(rolePlanner) >= 1 },
		"orchestrator never spawned the planner (it likely narrated the delegation without emitting sapaloq_spawn_plan)")

	plannerTask := waitForTaskWithRole(t, o, "planner", 30*time.Second)

	// Turn 2: user approves the plan. The orchestrator must now spawn the
	// agent with this plan id - again a real tool call, not narration.
	drainChat(t, o, ctx, "sim-1",
		"Oke rencananya bagus, lanjutkan eksekusinya. (plan task id: "+plannerTask.ID+")")

	waitFor(t, 30*time.Second, func() bool { return router.callCount(roleAgent) >= 1 },
		"orchestrator never spawned the agent after plan approval (narration-without-spawn regression)")

	agentTask := waitForTaskWithRole(t, o, "task-runner", 30*time.Second)
	waitFor(t, 30*time.Second, func() bool {
		rec, err := o.readTask(agentTask.ID)
		return err == nil && rec.Status == "done"
	}, "agent task did not reach done")
}

// ---------------------------------------------------------------------------
// Mode 2: live PLANNER drives real (sandboxed) tooling → writes a plan →
// finishes with a summary. No network sub-agents; tools operate on a temp dir.
// ---------------------------------------------------------------------------

func TestSimulatePlannerToolingToPlanSummary(t *testing.T) {
	real, entry := requireBlackbox(t)
	router := newRoleRouter(real, rolePlanner)
	o := newSimOrchestrator(t, router, entry)

	// Sandbox fixture the planner can inspect with real read/list tools.
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "main.go"),
		[]byte("package main\nfunc main() { println(\"hi\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := &taskRecord{
		ID:     "task-sim-planner",
		Role:   "planner",
		Status: "in_progress",
		Task: "Read " + filepath.Join(work, "main.go") +
			", then call write_plan with ## Goal/## Steps/## Acceptance describing how to add a greeting flag. Finish after writing the plan.",
	}
	// Materialize the task dir the way a real spawn does (spawnBackground →
	// writeTask), so write_plan's plan.md write lands on disk.
	if err := o.writeTask(*rec); err != nil {
		t.Fatal(err)
	}
	snap := o.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	o.runTaskActor(ctx, snap, "sim-2", rec)

	if rec.Status != "done" {
		t.Fatalf("planner status = %q, want done (err=%q)", rec.Status, rec.Error)
	}
	plan := o.readPlanMarkdown(rec.ID)
	if strings.TrimSpace(plan) == "" {
		t.Fatalf("planner produced no plan.md")
	}
	if !strings.Contains(strings.ToLower(plan), "## goal") {
		t.Fatalf("plan missing a Goal section:\n%s", plan)
	}
}

// ---------------------------------------------------------------------------
// Mode 3: live AGENT reads a handed-off plan → does real (sandboxed) work →
// finishes with a summary. The plan is a fixture; tools run on a temp dir.
// ---------------------------------------------------------------------------

func TestSimulateAgentReadPlanWorkSummary(t *testing.T) {
	real, entry := requireBlackbox(t)
	router := newRoleRouter(real, roleAgent)
	o := newSimOrchestrator(t, router, entry)

	// Pre-write a planner task with a plan.md so the agent can read it via the
	// hand-off (buildSubAgentMessages injects the approved plan).
	planTaskID := "task-sim-plan-fixture"
	if err := o.writeTask(taskRecord{
		ID: planTaskID, SessionID: "sim-3", Role: "planner", Status: "done",
		Task: "plan", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	target := filepath.Join(work, "hello.txt")
	plan := "## Goal\nCreate the file " + target + " containing the text `hello world`.\n\n" +
		"## Steps\n- [ ] Write `hello world` to " + target + "\n\n" +
		"## Acceptance\n- [ ] " + target + " exists and contains `hello world`"
	if err := os.MkdirAll(o.taskDir(planTaskID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.taskDir(planTaskID), "plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := &taskRecord{
		ID:         "task-sim-agent",
		SessionID:  "sim-3",
		Role:       "task-runner",
		Status:     "in_progress",
		PlanTaskID: planTaskID,
		Task:       "Execute the approved plan: create the file described in the plan, then complete the task.",
	}
	snap := o.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	o.runTaskActor(ctx, snap, "sim-3", rec)

	if rec.Status != "done" {
		t.Fatalf("agent status = %q, want done (err=%q)", rec.Status, rec.Error)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("agent did not create the planned file: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(body)), "hello world") {
		t.Fatalf("planned file content unexpected: %q", body)
	}
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

// drainChat runs one SendChat turn to completion, failing on a stream error.
func drainChat(t *testing.T, o *Orchestrator, ctx context.Context, sessionID, msg string) {
	t.Helper()
	stream, err := o.SendChat(ctx, sessionID, msg)
	if err != nil {
		t.Fatalf("SendChat: %v", err)
	}
	for ev := range stream {
		if ev.Kind == bridge.EventError {
			t.Fatalf("chat stream error: %s", ev.Error)
		}
	}
}

// waitFor polls cond until true or the deadline, then fails with msg.
func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal(msg)
}

// waitForTaskWithRole returns the most recent task of the given role, waiting up
// to d for one to appear.
func waitForTaskWithRole(t *testing.T, o *Orchestrator, role string, d time.Duration) taskRecord {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(o.tasksRoot())
		if err == nil {
			var best taskRecord
			var found bool
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				rec, rerr := o.readTask(e.Name())
				if rerr != nil || rec.Role != role {
					continue
				}
				if !found || rec.CreatedAt.After(best.CreatedAt) {
					best = rec
					found = true
				}
			}
			if found {
				return best
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no task with role %q appeared within %s", role, d)
	return taskRecord{}
}
