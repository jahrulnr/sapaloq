package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestAssistantContentPersistedForGeneration(t *testing.T) {
	turns := []chatstore.Turn{
		{Role: "assistant", Content: "hello", GenerationID: "1"},
		{Role: "assistant", Content: "[Called tools: sapaloq_stop]", GenerationID: "1"},
	}
	if !assistantContentPersistedForGeneration(turns, "1", "hello") {
		t.Fatal("expected hello to be found before stop note")
	}
	if assistantContentPersistedForGeneration(turns, "1", "goodbye") {
		t.Fatal("unexpected match for different content")
	}
	if assistantContentPersistedForGeneration(turns, "2", "hello") {
		t.Fatal("generation id filter should exclude hello")
	}
}

// TestChatFinalPersistNoDuplicateAfterStop locks the turns.json bug where a run
// that ends on sapaloq_stop after an earlier greeting re-persisted the greeting
// because the final guard only compared against the last assistant row (the stop
// note), not the full generation.
func TestChatFinalPersistNoDuplicateAfterStop(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "test", "model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTurnIDWithGeneration(ctx, sessionID, "user", "hai hai", 2, "1"); err != nil {
		t.Fatal(err)
	}

	fake := &durableTurnOrderBridge{}
	o := &Orchestrator{
		cfg:          config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		chat:         store,
		memoryDir:    t.TempDir(),
		vision:       make(map[string]bool),
		active:       make(map[string]*activeRun),
		sessionTasks: make(map[string]map[string]struct{}),
	}
	out := make(chan bridge.StreamEvent, 64)
	go func() {
		for range out {
		}
	}()
	cfg := turnConfig{
		sessionID: sessionID, runID: sessionID, generationID: "1",
		tools: []string{"exec", "sapaloq_stop"},
		sink:  chatSink{o: o, out: out}, recordToolTurns: true,
		dispatch: func(_ context.Context, call parse.ToolCall) turnOutcome {
			switch call.Name {
			case "exec":
				return turnOutcome{text: "exec ok", handled: true}
			case "sapaloq_stop":
				return turnOutcome{text: "stopped", handled: true, stop: true}
			default:
				return turnOutcome{}
			}
		},
	}
	assistant, err := o.runTurnLoop(ctx, providerSnapshot{
		cfg: o.cfg, entry: config.LLMBridge{Key: "test", Model: "model"}, br: fake,
	}, "hai hai", []bridge.Message{{Role: "user", Content: "hai hai"}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	close(out)

	trimmed := strings.TrimSpace(assistant.String())
	if trimmed == "" {
		t.Fatal("expected accumulated assistant text")
	}
	turns, err := store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSubstantiveAssistantForGeneration(turns, "1") {
		t.Fatal("runTurnLoop should have persisted substantive assistant text before final guard")
	}
	// Simulate chat.go final persist guard — must not append a duplicate row.
	if !shouldSkipFinalAssistantPersist(turns, "1", trimmed) {
		o.persistAssistantTurn(ctx, sessionID, trimmed, "1")
	}

	turns, err = store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	const want = "first answer"
	count := 0
	for _, tn := range turns {
		if tn.Role == "assistant" && strings.Contains(tn.Content, want) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("assistant rows containing %q = %d, want 1; turns=%+v", want, count, turns)
	}
}
