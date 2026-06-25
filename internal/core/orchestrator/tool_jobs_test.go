package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestToolJobSchedulerRunsIndependentJobsInParallel(t *testing.T) {
	s := newToolJobScheduler(t.TempDir(), 4, nil)
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	items := make([]scheduledTool, 3)
	for i := range items {
		index := i
		items[i] = scheduledTool{
			index: index,
			call:  parse.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"file"}`)},
			execute: func(context.Context) turnOutcome {
				started <- struct{}{}
				<-release
				return turnOutcome{text: "ok", handled: true}
			},
		}
	}
	results := s.submitBatch(context.Background(), "run-1", "session-1", items)
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("independent jobs did not start concurrently")
		}
	}
	close(release)
	count := 0
	for range results {
		count++
	}
	if count != 3 {
		t.Fatalf("results = %d, want 3", count)
	}
}

func TestToolJobSchedulerSerializesSameResourceLane(t *testing.T) {
	s := newToolJobScheduler(t.TempDir(), 4, nil)
	var mu sync.Mutex
	active := 0
	maxActive := 0
	items := make([]scheduledTool, 2)
	for i := range items {
		index := i
		items[i] = scheduledTool{
			index: index,
			call:  parse.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"/tmp/same-file"}`)},
			execute: func(context.Context) turnOutcome {
				mu.Lock()
				active++
				if active > maxActive {
					maxActive = active
				}
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				active--
				mu.Unlock()
				return turnOutcome{text: "ok", handled: true}
			},
		}
	}
	for range s.submitBatch(context.Background(), "run-1", "session-1", items) {
	}
	if maxActive != 1 {
		t.Fatalf("same resource lane ran %d jobs concurrently, want 1", maxActive)
	}
}

func TestActorInboxIsDurableAndDrainedAtSafePoint(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir()}
	want := actorControlEvent{
		Kind:      "steering.proposed",
		SessionID: "s1",
		SourceID:  "planner-1",
		TargetID:  "agent-1",
		Message:   "Use plan version 2.",
	}
	if err := o.enqueueActorEvent(want); err != nil {
		t.Fatal(err)
	}
	got := o.drainActorEvents("agent-1")
	if len(got) != 1 || got[0].Message != want.Message {
		t.Fatalf("events = %+v", got)
	}
	if again := o.drainActorEvents("agent-1"); len(again) != 0 {
		t.Fatalf("drain must acknowledge durable inbox, got %+v", again)
	}
}

type parallelCallsBridge struct {
	calls int
}

func (b *parallelCallsBridge) ID() string              { return "parallel-calls" }
func (b *parallelCallsBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *parallelCallsBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.calls++
	out := make(chan bridge.StreamEvent, 5)
	if b.calls == 1 {
		for i := 0; i < 3; i++ {
			call := parse.ToolCall{ID: string(rune('a' + i)), Name: "read_file", Arguments: json.RawMessage(`{"path":"x"}`)}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &call}
		}
	} else {
		out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "done"}
	}
	out <- bridge.StreamEvent{Kind: bridge.EventDone}
	close(out)
	return out, nil
}

func TestRunTurnLoopDispatchesProviderToolBatchInParallel(t *testing.T) {
	fake := &parallelCallsBridge{}
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		cfg:       config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		vision:    make(map[string]bool),
	}
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	out := make(chan bridge.StreamEvent, 32)
	go func() {
		for range out {
		}
	}()
	cfg := turnConfig{
		sessionID: "s1",
		runID:     "actor-1",
		tools:     []string{"read_file"},
		sink:      chatSink{o: o, out: out},
		dispatch: func(context.Context, parse.ToolCall) turnOutcome {
			started <- struct{}{}
			<-release
			return turnOutcome{text: "ok", handled: true}
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := o.runTurnLoop(context.Background(), providerSnapshot{
			cfg:   o.cfg,
			entry: config.LLMBridge{Key: "test", Model: "model"},
			br:    fake,
		}, "task", []bridge.Message{{Role: "user", Content: "go"}}, cfg)
		done <- err
	}()

	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("turn loop serialized provider tool calls")
		}
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("turn loop did not continue after parallel tool batch")
	}
	close(out)
}

type captureMessagesBridge struct {
	request bridge.Request
}

func (b *captureMessagesBridge) ID() string              { return "capture-messages" }
func (b *captureMessagesBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *captureMessagesBridge) Complete(_ context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.request = req
	out := make(chan bridge.StreamEvent, 2)
	out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "ack"}
	out <- bridge.StreamEvent{Kind: bridge.EventDone}
	close(out)
	return out, nil
}

func TestRunTurnLoopAppliesTargetedSteeringAtSafePoint(t *testing.T) {
	fake := &captureMessagesBridge{}
	o := &Orchestrator{stateDir: t.TempDir(), vision: make(map[string]bool)}
	if err := o.enqueueActorEvent(actorControlEvent{
		Kind:      "steering.proposed",
		SessionID: "s1",
		SourceID:  "planner-1",
		TargetID:  "agent-1",
		Message:   "Use the new API contract.",
	}); err != nil {
		t.Fatal(err)
	}
	out := make(chan bridge.StreamEvent, 8)
	go func() {
		for range out {
		}
	}()
	_, err := o.runTurnLoop(context.Background(), providerSnapshot{
		cfg:   config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}, "task", []bridge.Message{{Role: "user", Content: "implement"}}, turnConfig{
		sessionID: "s1",
		runID:     "agent-1",
		sink:      chatSink{o: o, out: out},
		dispatch:  func(context.Context, parse.ToolCall) turnOutcome { return turnOutcome{} },
	})
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for _, message := range fake.request.Messages {
		joined.WriteString(message.Content)
		joined.WriteByte('\n')
	}
	if !strings.Contains(joined.String(), "Use the new API contract.") {
		t.Fatalf("steering missing from inference context: %s", joined.String())
	}
	if pending := o.drainActorEvents("agent-1"); len(pending) != 0 {
		t.Fatalf("steering was not acknowledged at the safe point: %+v", pending)
	}
}
