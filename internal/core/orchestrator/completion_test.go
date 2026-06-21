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
