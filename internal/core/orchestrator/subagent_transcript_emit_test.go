package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestSubagentEmitUsesDeltaPatches(t *testing.T) {
	o := &Orchestrator{}
	sink := &subagentSink{o: o, taskID: "task-delta", parentSessionID: "chat-1"}
	sink.coalescer = NewTranscriptCoalescer("task-delta")
	out := make(chan bridge.StreamEvent, 8)
	ctx := context.Background()
	opts := transcriptEmitOpts{
		sessionID:          "chat-1",
		actorID:            "task-delta",
		parentSessionID:    "chat-1",
		generationID:       "task-delta",
		coalescer:          sink.coalescer,
		patchState:         &sink.widgetPatchState,
		patchMu:            &sink.patchMu,
		out:                out,
		mergePersistedBase: false,
	}

	if !o.emitCoalescedTranscript(ctx, opts, bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "a"}) {
		t.Fatal("first delta should send")
	}
	if !o.emitCoalescedTranscript(ctx, opts, bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "b"}) {
		t.Fatal("throttled delta should return true")
	}
	if len(out) != 1 {
		t.Fatalf("immediate patch count = %d, want throttle", len(out))
	}
	first := <-out
	if first.Transcript == nil || first.Transcript.Mode != bridge.TranscriptPatchDelta {
		t.Fatalf("first patch = %+v", first.Transcript)
	}
	if first.Transcript.ActorID != "task-delta" {
		t.Fatalf("actor_id = %q", first.Transcript.ActorID)
	}

	time.Sleep(deltaWidgetPatchMinInterval + 15 * time.Millisecond)
	if len(out) < 1 {
		t.Fatal("expected scheduled flush patch")
	}
	flush := <-out
	if flush.Transcript == nil || flush.Transcript.Mode != bridge.TranscriptPatchDelta {
		t.Fatalf("flush patch = %+v", flush.Transcript)
	}
	foundB := false
	for _, op := range flush.Transcript.Ops {
		if op.Delta == "b" {
			foundB = true
		}
	}
	if !foundB {
		t.Fatalf("flush ops missing b: %+v", flush.Transcript.Ops)
	}
}

func TestSubagentSinkToolUpdateSnapshot(t *testing.T) {
	o := &Orchestrator{}
	sink := &subagentSink{o: o, taskID: "task-tool", parentSessionID: "chat-1"}
	out := make(chan bridge.StreamEvent, 4)
	opts := transcriptEmitOpts{
		sessionID:          "chat-1",
		actorID:            "task-tool",
		parentSessionID:    "chat-1",
		generationID:       "task-tool",
		coalescer:          NewTranscriptCoalescer("task-tool"),
		patchState:         &sink.widgetPatchState,
		patchMu:            &sink.patchMu,
		out:                out,
		mergePersistedBase: false,
	}
	call := parse.ToolCall{Name: "exec", ID: "t1"}
	toolEv := bridge.StreamEvent{
		Kind:       bridge.EventToolUpdate,
		ToolCall:   &call,
		ToolResult: "ok",
		Status:     "completed",
	}
	opts.coalescer.Apply(toolEv)
	if !o.emitCoalescedTranscript(context.Background(), opts, toolEv) {
		t.Fatal("tool snapshot should send")
	}
	ev := <-out
	if ev.Transcript == nil || ev.Transcript.Mode == bridge.TranscriptPatchDelta {
		t.Fatalf("tool update should snapshot: %+v", ev.Transcript)
	}
	if len(ev.Transcript.Entries) == 0 {
		t.Fatal("expected tool entry in snapshot")
	}
}
