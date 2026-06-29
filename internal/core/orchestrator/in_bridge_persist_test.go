package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestPersistInBridgeToolUpdateWritesAssistantAndToolTurns(t *testing.T) {
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
	o.persistInBridgeToolUpdate(ctx, sessionID, "2", cfg, &resp, &strings.Builder{}, &msgs, ev)

	turns, err := store.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	var assistants, tools int
	for _, tr := range turns {
		if tr.GenerationID != "2" {
			continue
		}
		switch tr.Role {
		case "assistant":
			assistants++
			if !strings.Contains(tr.Content, "Called tools: exec") {
				t.Fatalf("assistant missing tool note: %q", tr.Content)
			}
			if !strings.Contains(tr.Content, "agent-browser") {
				t.Fatalf("assistant missing narration: %q", tr.Content)
			}
		case "tool":
			tools++
			if !strings.Contains(tr.Content, "Beranda") {
				t.Fatalf("tool result missing: %q", tr.Content)
			}
		}
	}
	if assistants != 1 || tools != 1 {
		t.Fatalf("generation 2 turns: assistants=%d tools=%d, want 1 each", assistants, tools)
	}
	if len(msgs) != 3 {
		t.Fatalf("cleanMessages len = %d, want user+assistant+tool", len(msgs))
	}
	if resp.Len() != 0 {
		t.Fatalf("response buffer not reset after persist")
	}
}

func TestPersistInBridgeToolUpdateSkipsOrchestratorTools(t *testing.T) {
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
	o.persistInBridgeToolUpdate(ctx, sessionID, "1", turnConfig{recordToolTurns: true}, &strings.Builder{}, &strings.Builder{}, &msgs, ev)
	turns, _ := store.ActiveTurns(ctx, sessionID, false)
	if len(turns) != 0 {
		t.Fatalf("expected no turns for non in-bridge source, got %d", len(turns))
	}
}
