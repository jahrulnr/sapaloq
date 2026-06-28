package orchestrator

import (
	"context"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestEmitWidgetDeltaPatchOnResponseDelta(t *testing.T) {
	o := &Orchestrator{active: make(map[string]*activeRun)}
	sessionID := "sess-delta"
	genID := "7"
	coalescer := NewTranscriptCoalescer(genID)
	o.active[sessionID] = &activeRun{id: 7, coalescer: coalescer}
	out := make(chan bridge.StreamEvent, 4)
	ctx := context.Background()

	if !o.emitWidget(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: "hello"}) {
		t.Fatal("delta patch should send")
	}
	ev := <-out
	if ev.Kind != bridge.EventTranscript || ev.Transcript == nil {
		t.Fatalf("event = %+v", ev)
	}
	p := ev.Transcript
	if p.Mode != bridge.TranscriptPatchDelta {
		t.Fatalf("mode = %q", p.Mode)
	}
	if len(p.Ops) < 2 {
		t.Fatalf("ops = %+v, want upsert+append", p.Ops)
	}
	if p.Ops[0].Op != "upsert" || p.Ops[0].Entry.ID != genID+"-pending-text" {
		t.Fatalf("upsert = %+v", p.Ops[0])
	}
	if p.Ops[1].Op != "append_text" || p.Ops[1].Delta != "hello" {
		t.Fatalf("append = %+v", p.Ops[1])
	}
}

func TestEmitWidgetSnapshotOnDone(t *testing.T) {
	o := &Orchestrator{active: make(map[string]*activeRun)}
	sessionID := "sess-done"
	genID := "9"
	coalescer := NewTranscriptCoalescer(genID)
	o.active[sessionID] = &activeRun{
		id:        9,
		coalescer: coalescer,
		transcriptBase: []bridge.TranscriptEntry{
			{ID: "u1", Kind: bridge.TranscriptUser, Text: "hey"},
		},
	}
	out := make(chan bridge.StreamEvent, 2)
	ctx := context.Background()
	_ = o.emitWidget(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventResponseDelta, SessionID: sessionID, Delta: "ok"})
	<-out
	toolCall := bridge.NewEvent(bridge.EventToolCall)
	toolCall.ToolCall = &parse.ToolCall{ID: "tc-1", Name: "exec", Arguments: []byte(`{"command":"ls"}`)}
	_ = o.emitWidget(ctx, out, sessionID, toolCall)
	<-out
	toolDone := bridge.NewEvent(bridge.EventToolUpdate)
	toolDone.ToolCall = &parse.ToolCall{ID: "tc-1", Name: "exec"}
	toolDone.ToolResult = "ok\n"
	toolDone.Status = "completed"
	_ = o.emitWidget(ctx, out, sessionID, toolDone)
	<-out

	if !o.emitWidget(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID}) {
		t.Fatal("done patch should send")
	}
	ev := <-out
	if ev.Transcript == nil || ev.Transcript.Mode != bridge.TranscriptPatchSnapshot {
		t.Fatalf("snapshot patch = %+v", ev.Transcript)
	}
	if !ev.Transcript.Finished {
		t.Fatal("done patch should be finished")
	}
	var sawTool bool
	for _, e := range ev.Transcript.Entries {
		if e.Kind == bridge.TranscriptTool && e.ToolName == "exec" && e.ToolResult == "ok\n" {
			sawTool = true
		}
	}
	if !sawTool {
		t.Fatalf("done snapshot missing completed tool: %+v", ev.Transcript.Entries)
	}
}
