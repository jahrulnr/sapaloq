package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func TestEmitChatTerminalErrorSendsDoneAfterError(t *testing.T) {
	o := &Orchestrator{}
	out := make(chan bridge.StreamEvent, 4)
	o.emitChatTerminalError(context.Background(), out, "s1", errors.New("inference-turn budget exhausted after 128 turns"))
	if len(out) != 2 {
		t.Fatalf("events = %d, want 2 (error + done)", len(out))
	}
	ev1 := <-out
	ev2 := <-out
	if ev1.Kind != bridge.EventError {
		t.Fatalf("first kind = %q, want error", ev1.Kind)
	}
	if ev2.Kind != bridge.EventDone {
		t.Fatalf("second kind = %q, want done", ev2.Kind)
	}
}

func TestEmitChatTerminalErrorSkipsWhenStreamAlreadySurfaced(t *testing.T) {
	o := &Orchestrator{}
	out := make(chan bridge.StreamEvent, 4)
	o.emitChatTerminalError(context.Background(), out, "s1", errors.Join(errors.New("provider 500"), errStreamErrorSurfaced))
	if len(out) != 0 {
		t.Fatalf("events = %d, want 0 when stream error already surfaced", len(out))
	}
}

func TestEmitChatTerminalErrorSetsTimestamp(t *testing.T) {
	o := &Orchestrator{}
	out := make(chan bridge.StreamEvent, 2)
	before := time.Now().UTC().Add(-time.Second)
	o.emitChatTerminalError(context.Background(), out, "s1", errors.New("boom"))
	<-out
	done := <-out
	if done.At.Before(before) {
		t.Fatalf("done timestamp not set")
	}
}
