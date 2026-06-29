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
	first := <-out
	if first.Transcript == nil || first.Transcript.Mode != bridge.TranscriptPatchDelta {
		t.Fatalf("first patch mode = %+v", first.Transcript)
	}
	flush := <-out
	if flush.Transcript == nil || flush.Transcript.Mode != bridge.TranscriptPatchDelta {
		t.Fatalf("flush patch = %+v", flush.Transcript)
	}
	if len(flush.Transcript.Ops) == 0 || flush.Transcript.Ops[len(flush.Transcript.Ops)-1].Delta != "b" {
		t.Fatalf("flush ops = %+v", flush.Transcript.Ops)
	}
}

func TestEmitWidgetDoesNotOvertakeScheduledDeltaFlush(t *testing.T) {
	o := &Orchestrator{active: make(map[string]*activeRun)}
	const sessionID = "sess-ordered-flush"
	run := &activeRun{id: 1, coalescer: NewTranscriptCoalescer("1")}
	run.lastDeltaPatch = time.Now().Add(-deltaWidgetPatchMinInterval)
	run.deltaFlushScheduled = true
	run.pendingTextFlush.WriteString("older")
	o.active[sessionID] = run
	out := make(chan bridge.StreamEvent, 4)

	if !o.emitWidget(context.Background(), out, sessionID, bridge.StreamEvent{
		Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: "newer",
	}) {
		t.Fatal("new delta should remain accepted")
	}
	if len(out) != 0 {
		t.Fatal("new delta overtook the already scheduled older flush")
	}

	opts := transcriptEmitOpts{
		sessionID: sessionID, generationID: "1", coalescer: run.coalescer,
		patchState: &run.widgetPatchState, patchMu: &o.activeMu,
		emitMu: &run.deltaEmitMu, out: out,
	}
	o.flushCoalescedDeltaPatch(context.Background(), opts)
	patch := <-out
	if patch.Transcript == nil || len(patch.Transcript.Ops) == 0 {
		t.Fatalf("flush patch = %+v", patch.Transcript)
	}
	got := patch.Transcript.Ops[len(patch.Transcript.Ops)-1].Delta
	if got != "oldernewer" {
		t.Fatalf("ordered delta = %q, want %q", got, "oldernewer")
	}
}
