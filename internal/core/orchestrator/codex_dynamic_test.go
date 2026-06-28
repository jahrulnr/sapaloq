package orchestrator

import (
	"context"
	"sync"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

type codexCallbackBridge struct{ calls int }

func (b *codexCallbackBridge) ID() string              { return "codex-bridge" }
func (b *codexCallbackBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *codexCallbackBridge) Complete(ctx context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.calls++
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		call := parse.ToolCall{ID: "stop-1", Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`), Source: "codex"}
		out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &call}
		if req.ToolExecutor == nil {
			out <- bridge.StreamEvent{Kind: bridge.EventError, Error: "missing ToolExecutor"}
			return
		}
		if _, err := req.ToolExecutor(ctx, call); err != nil {
			out <- bridge.StreamEvent{Kind: bridge.EventError, Error: err.Error()}
			return
		}
		out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "finished"}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

type captureTurnSink struct {
	mu     sync.Mutex
	events []bridge.StreamEvent
}

func (s *captureTurnSink) emit(_ context.Context, ev bridge.StreamEvent) {
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
}
func (*captureTurnSink) beat(string) {}

func TestCodexDynamicToolExecutesOnceAndStopsRun(t *testing.T) {
	fake := &codexCallbackBridge{}
	sink := &captureTurnSink{}
	o := &Orchestrator{memoryDir: t.TempDir(), vision: make(map[string]bool), active: make(map[string]*activeRun)}
	dispatches := 0
	result, err := o.runTurnLoop(context.Background(), providerSnapshot{
		cfg: config.Config{}, entry: config.LLMBridge{Key: "codex", Model: "model"}, br: fake,
	}, "task", []bridge.Message{{Role: "user", Content: "work"}}, turnConfig{
		sessionID: "session", runID: "session", tools: []string{"sapaloq_stop"}, sink: sink,
		dispatch: func(context.Context, parse.ToolCall) turnOutcome {
			dispatches++
			return turnOutcome{handled: true, stop: true}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.String() != "finished" || fake.calls != 1 || dispatches != 1 {
		t.Fatalf("result=%q bridge calls=%d dispatches=%d", result.String(), fake.calls, dispatches)
	}
	var telemetry int
	for _, ev := range sink.events {
		if ev.Kind == bridge.EventToolCall && ev.ToolCall != nil && ev.ToolCall.Source == "codex" {
			telemetry++
		}
	}
	if telemetry != 1 {
		t.Fatalf("codex tool telemetry events = %d, want 1", telemetry)
	}
}
