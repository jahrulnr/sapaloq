package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
	"github.com/jahrulnr/sapaloq/internal/parse/artifacts"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestAppendInBridgeToolUpdateWritesWireOrder(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "cursor", "default")
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{chat: store}
	var resp strings.Builder
	resp.WriteString("Menjalankan verifikasi visual dengan agent-browser.")
	cfg := turnConfig{recordToolTurns: true, taskAnchor: "test ui"}
	msgs := []bridge.Message{{Role: "user", Content: "sip, test"}}
	ev := bridge.StreamEvent{
		Kind:       bridge.EventToolUpdate,
		ToolCall:   &parse.ToolCall{ID: "t1", Name: "exec", Source: "cursor"},
		ToolResult: "✓ Beranda | BangunSoft",
		Status:     "completed",
	}
	o.appendInBridgeToolUpdate(ctx, sessionID, "2", cfg, &resp, &strings.Builder{}, &msgs, ev)

	turns, err := store.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want tool then assistant", len(turns))
	}
	if turns[0].Role != "tool" || turns[1].Role != "assistant" {
		t.Fatalf("wire order = %s,%s want tool,assistant", turns[0].Role, turns[1].Role)
	}
	if !strings.Contains(turns[1].Content, "Called tools: exec") {
		t.Fatalf("assistant missing tool note: %q", turns[1].Content)
	}
	if len(msgs) != 3 {
		t.Fatalf("cleanMessages len = %d, want user+tool+assistant", len(msgs))
	}
	if resp.Len() != 0 {
		t.Fatalf("response buffer not reset after persist")
	}
}

func TestAppendInBridgeToolUpdateStopOnlyWireOrder(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "cursor", "default")
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{chat: store}
	cfg := turnConfig{recordToolTurns: true, foregroundOrchestrator: true}
	msgs := []bridge.Message{{Role: "user", Content: "hai"}}
	ev := bridge.StreamEvent{
		Kind:       bridge.EventToolUpdate,
		ToolCall:   &parse.ToolCall{ID: "stop-1", Name: "sapaloq_stop", Source: "cursor"},
		ToolResult: "Stopped: done",
		Status:     "completed",
	}
	var thinking strings.Builder
	thinking.WriteString("planning greeting")
	o.appendInBridgeToolUpdate(ctx, sessionID, "1", cfg, &strings.Builder{}, &thinking, &msgs, ev)

	turns, _ := store.ActiveTurns(ctx, sessionID, false)
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want thinking+tool (note-only assistant deferred to stop)", len(turns))
	}
	if turns[0].Role != "thinking" || turns[1].Role != "tool" {
		t.Fatalf("order = %s,%s want thinking,tool", turns[0].Role, turns[1].Role)
	}

	greeting := artifacts.FallbackAskGreeting()
	o.persistAssistantTurn(ctx, sessionID, greeting+"\n\n"+calledToolsNote([]scheduledTool{{call: *ev.ToolCall}}), "1")

	turns, err = store.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 3 || turns[2].Role != "assistant" || !strings.Contains(turns[2].Content, greeting) {
		t.Fatalf("turns after greeting = %+v", turns)
	}
}

func TestAppendInBridgeToolUpdateSkipsOrchestratorTools(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{chat: store}
	msgs := []bridge.Message{}
	ev := bridge.StreamEvent{
		Kind:       bridge.EventToolUpdate,
		ToolCall:   &parse.ToolCall{ID: "t1", Name: "exec", Source: "orchestrator"},
		ToolResult: "ok",
		Status:     "completed",
	}
	o.appendInBridgeToolUpdate(ctx, sessionID, "1", turnConfig{recordToolTurns: true}, &strings.Builder{}, &strings.Builder{}, &msgs, ev)
	turns, _ := store.ActiveTurns(ctx, sessionID, false)
	if len(turns) != 0 {
		t.Fatalf("expected no turns for non in-bridge source, got %d", len(turns))
	}
}

func TestReplayMapper_AssistantBeforeTool(t *testing.T) {
	turns := []chatstore.Turn{
		{Role: "user", Content: "go"},
		{Role: "tool", Content: "[Tool results]\noutput", GenerationID: "1"},
		{Role: "assistant", Content: "done\n\n[Called tools: exec]", GenerationID: "1"},
	}
	msgs := actorTurnsToMessages(turns)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want user+assistant+tool", len(msgs))
	}
	if msgs[1].Role != "assistant" || msgs[2].Role != "tool" {
		t.Fatalf("replay order = %s,%s want assistant,tool", msgs[1].Role, msgs[2].Role)
	}
}

func TestReplayMapper_SkipsThinking(t *testing.T) {
	turns := []chatstore.Turn{
		{Role: "user", Content: "hi"},
		{Role: "thinking", Content: "hmm"},
		{Role: "assistant", Content: "hello"},
	}
	msgs := actorTurnsToMessages(turns)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
}

func TestReplayMapper_ToolAfterAssistantWhenWireOrderOpposite(t *testing.T) {
	turns := []chatstore.Turn{
		{Role: "user", Content: "run"},
		{Role: "assistant", Content: "ok\n\n[Called tools: exec]", GenerationID: "2"},
		{Role: "tool", Content: "[Tool results]\nok", GenerationID: "2"},
	}
	msgs := actorTurnsToMessages(turns)
	if len(msgs) != 3 || msgs[2].Role != "tool" {
		t.Fatalf("replay = %+v", msgs)
	}
}
