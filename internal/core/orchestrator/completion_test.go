package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// speakEnabledCfg returns a config with SpeakOnTerminal explicitly on (the
// behavior is opt-in for a zero-value Config; DefaultConfig turns it on).
func speakEnabledCfg() config.Config {
	return config.Config{Orchestrator: config.OrchestratorConfig{
		Completion: config.CompletionConfig{SpeakOnTerminal: true},
	}}
}

// TestTerminalCompletionSpeaksIntoSession is the regression test for THE bug:
// a background task that finishes must produce a spoken assistant turn in its
// session (not just a task card), so a completion landing after sapaloq_wait
// returns is still surfaced in chat.
func TestTerminalCompletionSpeaksIntoSession(t *testing.T) {
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, err := store.ActiveSession(ctx, "k", "m")
	if err != nil {
		t.Fatalf("active session: %v", err)
	}

	b := bus.New()
	events, cancel := b.Subscribe(8)
	defer cancel()
	o := &Orchestrator{chat: store, bus: b, cfg: speakEnabledCfg()}

	record := taskRecord{ID: "task-done-1", SessionID: sessionID, Role: "task-runner", Status: "done", Result: "Website built."}
	o.publishTaskUpdate(sessionID, record)

	// The completion must be persisted as an assistant turn.
	turns, err := store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatalf("active turns: %v", err)
	}
	var spoke bool
	for _, tn := range turns {
		if tn.Role == "assistant" && strings.Contains(tn.Content, "task-done-1") && strings.Contains(tn.Content, "selesai") {
			spoke = true
		}
	}
	if !spoke {
		t.Fatalf("terminal completion was not spoken into the session; turns=%+v", turns)
	}

	// And it must be republished as a streamed response event for live widgets.
	sawResponse := false
	for drained := false; !drained; {
		select {
		case ev := <-events:
			if ev.Data.Kind == bridge.EventResponseDelta && strings.Contains(ev.Data.Delta, "task-done-1") {
				sawResponse = true
			}
		default:
			drained = true
		}
	}
	if !sawResponse {
		t.Fatalf("terminal completion was not republished as a response event")
	}
}

// TestTerminalCompletionSpokenOnce verifies idempotency: republishing the same
// terminal task (e.g. watchdog + runBackgroundTask both push) must not produce
// duplicate chat bubbles.
func TestTerminalCompletionSpokenOnce(t *testing.T) {
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, err := store.ActiveSession(ctx, "k", "m")
	if err != nil {
		t.Fatalf("active session: %v", err)
	}
	o := &Orchestrator{chat: store, bus: bus.New(), cfg: speakEnabledCfg()}

	record := taskRecord{ID: "task-once-1", SessionID: sessionID, Role: "task-runner", Status: "failed", Error: "boom"}
	o.publishTaskUpdate(sessionID, record)
	o.publishTaskUpdate(sessionID, record) // duplicate push

	turns, err := store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatalf("active turns: %v", err)
	}
	count := 0
	for _, tn := range turns {
		if tn.Role == "assistant" && strings.Contains(tn.Content, "task-once-1") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("spoke %d times, want exactly 1 (idempotent)", count)
	}
}

// TestRunBackgroundTaskSpeaksCompletion exercises the full integrated path:
// runBackgroundTask drives the sub-agent loop to a terminal tool call and the
// completion must be spoken into the session. This is the end-to-end proof that
// the "agent finished but nobody told the user" bug is fixed.
func TestRunBackgroundTaskSpeaksCompletion(t *testing.T) {
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, _ := store.ActiveSession(ctx, "k", "m")

	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		{toolCallEvent("sapaloq_complete_task", map[string]any{"summary": "Built the thing."})},
	}}
	dir := t.TempDir()
	o := &Orchestrator{
		memoryDir:  dir,
		workersDir: filepath.Join(dir, "workers"),
		workers:    newWorkerRegistry(filepath.Join(dir, "workers")),
		progress:   ProgressWriter{Dir: t.TempDir()},
		chat:       store,
		bus:        bus.New(),
		cfg:        speakEnabledCfg(),
	}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "k", Model: "m"}, br: fake}
	rec := taskRecord{ID: "task-int-1", SessionID: sessionID, Role: "task-runner", Status: "pending", Task: "do it"}

	bgCtx, cancel := context.WithCancel(ctx)
	o.taskCancels = map[string]context.CancelFunc{rec.ID: cancel}
	o.sessionTasks = map[string]map[string]struct{}{sessionID: {rec.ID: {}}}
	o.runBackgroundTask(bgCtx, cancel, snap, sessionID, rec)

	got, err := o.readTask(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("status = %q, want done", got.Status)
	}
	turns, _ := store.ActiveTurns(ctx, sessionID, true)
	spoke := false
	for _, tn := range turns {
		if tn.Role == "assistant" && strings.Contains(tn.Content, "task-int-1") && strings.Contains(tn.Content, "selesai") {
			spoke = true
		}
	}
	if !spoke {
		t.Fatalf("integrated completion not spoken; turns=%+v", turns)
	}
}

// TestCompletionAnnouncementAuthoredByLLM proves the redundancy fix: when a
// provider is available the spoken completion is AUTHORED BY THE ORCHESTRATOR
// (its own words), not a verbatim copy of the sub-agent's raw result. The card
// stays a terse status line; this bubble carries the natural summary.
func TestCompletionAnnouncementAuthoredByLLM(t *testing.T) {
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, err := store.ActiveSession(ctx, "k", "m")
	if err != nil {
		t.Fatalf("active session: %v", err)
	}

	const rawResult = "RAW-SUBAGENT-DUMP-12345 lots of file listings and structure"
	const authored = "Sub-agent-nya udah kelar, hasilnya website portfolio jadi di /tmp/profile."
	// The announcer turn: the orchestrator talks (no tool call), just narrates.
	fake := &scriptedBridge{turns: [][]bridge.StreamEvent{
		{{Kind: bridge.EventResponseDelta, Delta: authored}},
	}}

	b := bus.New()
	events, cancel := b.Subscribe(8)
	defer cancel()
	o := &Orchestrator{chat: store, bus: b, cfg: speakEnabledCfg(), bridge: fake}

	record := taskRecord{ID: "task-llm-1", SessionID: sessionID, Role: "task-runner", Status: "done", Result: rawResult}
	o.publishTaskUpdate(sessionID, record)

	// The persisted assistant turn must be the LLM's wording, not the raw dump.
	turns, err := store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatalf("active turns: %v", err)
	}
	var spoken string
	for _, tn := range turns {
		if tn.Role == "assistant" {
			spoken = tn.Content
		}
	}
	if !strings.Contains(spoken, authored) {
		t.Fatalf("assistant turn should contain the LLM-authored text; got %q", spoken)
	}
	if strings.Contains(spoken, rawResult) {
		t.Fatalf("assistant turn must NOT copy-paste the raw sub-agent result; got %q", spoken)
	}

	// The republished response event must carry the authored text + task id.
	sawAuthored := false
	for drained := false; !drained; {
		select {
		case ev := <-events:
			if ev.Data.Kind == bridge.EventResponseDelta && ev.Data.TaskID == "task-llm-1" && strings.Contains(ev.Data.Delta, authored) {
				sawAuthored = true
			}
		default:
			drained = true
		}
	}
	if !sawAuthored {
		t.Fatalf("authored completion was not republished as a task-tagged response event")
	}
}

// TestCompletionFallsBackToTemplateWithoutProvider ensures that with no bridge
// configured (headless), a completion is still surfaced via the plain template
// instead of being silently dropped.
func TestCompletionFallsBackToTemplateWithoutProvider(t *testing.T) {
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, _ := store.ActiveSession(ctx, "k", "m")
	o := &Orchestrator{chat: store, bus: bus.New(), cfg: speakEnabledCfg()} // bridge == nil

	o.publishTaskUpdate(sessionID, taskRecord{ID: "task-fallback-1", SessionID: sessionID, Role: "task-runner", Status: "done", Result: "ok"})

	turns, _ := store.ActiveTurns(ctx, sessionID, true)
	spoke := false
	for _, tn := range turns {
		if tn.Role == "assistant" && strings.Contains(tn.Content, "task-fallback-1") && strings.Contains(tn.Content, "selesai") {
			spoke = true
		}
	}
	if !spoke {
		t.Fatalf("without a provider the template fallback must still speak the completion")
	}
}

// TestTaskUpdateEventDoneIsStatusOnly locks in that the task CARD is a status
// timeline: a done update must NOT embed the (potentially huge) raw result —
// that summary belongs only to the orchestrator-authored bubble.
func TestTaskUpdateEventDoneIsStatusOnly(t *testing.T) {
	const rawResult = "VERY LONG RESULT DUMP that must never appear on the status card"
	ev := taskUpdateEvent("sess-1", taskRecord{ID: "task-card-1", Role: "task-runner", Status: "done", Result: rawResult})
	if ev.Kind == "" {
		t.Fatalf("expected a task_update event")
	}
	if strings.Contains(ev.Summary, rawResult) {
		t.Fatalf("done card must be status-only; summary leaked the raw result: %q", ev.Summary)
	}
	if strings.TrimSpace(ev.Summary) == "" {
		t.Fatalf("done card should still carry a short status label")
	}
}

// recordingBridge captures the requests it is asked to complete so a test can
// assert what system/user prompt the orchestrator authored. It always replies
// with a single fixed text delta + done (no tool calls).
type recordingBridge struct {
	requests []bridge.Request
	reply    string
}

func (b *recordingBridge) ID() string              { return "recording" }
func (b *recordingBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *recordingBridge) Complete(_ context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.requests = append(b.requests, req)
	out := make(chan bridge.StreamEvent, 2)
	go func() {
		defer close(out)
		reply := b.reply
		if reply == "" {
			reply = "ok"
		}
		out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: reply}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// TestTaskRunnerCompletionAnnouncementUnchanged guards that a normal (non-
// planner) completion keeps the original "report the result" framing — the
// planner branch must not bleed into ordinary task completions.
func TestTaskRunnerCompletionAnnouncementUnchanged(t *testing.T) {
	fake := &recordingBridge{reply: "Udah kelar."}
	o := &Orchestrator{cfg: speakEnabledCfg(), bridge: fake}

	record := taskRecord{ID: "task-run-1", Role: "task-runner", Status: "done", Task: "build", Result: "done"}
	_ = o.composeCompletionAnnouncement("sess-1", record)

	if len(fake.requests) == 0 {
		t.Fatal("announcer never called the bridge")
	}
	var sys string
	for _, m := range fake.requests[0].Messages {
		if m.Role == "system" {
			sys = strings.ToLower(m.Content)
			break
		}
	}
	// The task-runner branch must NOT ask for plan review/approval.
	if strings.Contains(sys, "rencana") || strings.Contains(sys, "review") {
		t.Fatalf("task-runner completion must not use the planner review framing; got: %q", sys)
	}
}

// TestSpeakDisabledByDefaultConfig confirms a zero-value config does NOT speak
// (opt-in), so existing tests/behavior are unaffected.
func TestSpeakDisabledForZeroConfig(t *testing.T) {
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, _ := store.ActiveSession(ctx, "k", "m")
	o := &Orchestrator{chat: store, bus: bus.New(), cfg: config.Config{}}

	o.publishTaskUpdate(sessionID, taskRecord{ID: "task-quiet-1", SessionID: sessionID, Role: "task-runner", Status: "done", Result: "ok"})

	turns, _ := store.ActiveTurns(ctx, sessionID, true)
	for _, tn := range turns {
		if tn.Role == "assistant" && strings.Contains(tn.Content, "task-quiet-1") {
			t.Fatalf("speak should be opt-in; zero-value config must stay quiet")
		}
	}
}
