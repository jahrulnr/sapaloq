package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func TestEmitWidgetThrottlesDeltaPatches(t *testing.T) {
	o := &Orchestrator{active: make(map[string]*activeRun)}
	sessionID := "sess-throttle"
	genID := "1"
	coalescer := NewTranscriptCoalescer(genID)
	o.active[sessionID] = &activeRun{
		id:        1,
		coalescer: coalescer,
		transcriptBase: []bridge.TranscriptEntry{
			{ID: "base-user", Kind: bridge.TranscriptUser, Text: "hi"},
		},
	}
	out := make(chan bridge.StreamEvent, 8)
	ctx := context.Background()

	ev := bridge.StreamEvent{Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: "a"}
	if !o.emitWidget(ctx, out, sessionID, ev) {
		t.Fatal("first delta patch should send")
	}
	if len(out) != 1 {
		t.Fatalf("first patch count = %d", len(out))
	}
	if !o.emitWidget(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: "b"}) {
		t.Fatal("throttled delta should still return true")
	}
	if len(out) != 1 {
		t.Fatalf("immediate second patch count = %d, want throttle", len(out))
	}
	time.Sleep(deltaWidgetPatchMinInterval + 10*time.Millisecond)
	if len(out) < 2 {
		t.Fatalf("scheduled flush count = %d, want >= 2", len(out))
	}
}
