package orchestrator

import (
	"context"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestEmitSessionResetPatch(t *testing.T) {
	dir := t.TempDir()
	store, err := chatstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sessionID, err := store.ActiveSession(ctx, "test", "model")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.AppendTurn(ctx, sessionID, "user", "hello", 10)
	_ = store.AppendTurn(ctx, sessionID, "assistant", "hi", 10)

	o := &Orchestrator{chat: store, active: make(map[string]*activeRun)}
	out := make(chan bridge.StreamEvent, 8)
	runID := o.setActiveGeneration(sessionID, func() {})
	o.migrateActiveRun(sessionID, "chat-new", runID)
	o.emitSlash(ctx, out, "chat-new", responseEvent("chat-new", "Session reset."))
	if !o.emitSessionReset(ctx, out, "chat-new", runID, true) {
		t.Fatal("emitSessionReset failed")
	}
	var resetPatch *bridge.TranscriptPatch
	for len(out) > 0 {
		ev := <-out
		if ev.Kind == bridge.EventTranscript && ev.Transcript != nil && ev.Transcript.Reset {
			resetPatch = ev.Transcript
		}
	}
	if resetPatch == nil {
		t.Fatal("expected reset transcript patch")
	}
	if resetPatch.SessionID != "chat-new" {
		t.Fatalf("session=%q", resetPatch.SessionID)
	}
	if !resetPatch.Finished {
		t.Fatal("expected finished reset patch")
	}
}
