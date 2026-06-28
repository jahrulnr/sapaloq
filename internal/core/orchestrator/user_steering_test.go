package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestUserSteeringEnqueuesToSessionInbox(t *testing.T) {
	o := &Orchestrator{
		stateDir: t.TempDir(),
		active: map[string]*activeRun{
			"session-1": {id: 1, cancel: func() {}},
		},
	}

	if err := o.UserSteering(context.Background(), " session-1 ", " Use the JSON API. "); err != nil {
		t.Fatal(err)
	}
	events := o.drainActorEvents("session-1")
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one steering event", events)
	}
	got := events[0]
	if got.Kind != "steering.proposed" || got.SessionID != "session-1" || got.TargetID != "session-1" || got.SourceID != "user" {
		t.Fatalf("unexpected steering envelope: %+v", got)
	}
	if got.Message != "Use the JSON API." || got.Priority != "normal" {
		t.Fatalf("unexpected steering payload: %+v", got)
	}
}

func TestUserSteeringIsAppliedAtNextInferenceSafePoint(t *testing.T) {
	fake := &captureMessagesBridge{}
	o := &Orchestrator{
		stateDir: t.TempDir(),
		vision:   make(map[string]bool),
		active:   map[string]*activeRun{"session-1": {id: 1, cancel: func() {}}},
	}
	if err := o.UserSteering(context.Background(), "session-1", "Do not edit generated files."); err != nil {
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
		sessionID: "session-1",
		runID:     "session-1",
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
	if !strings.Contains(joined.String(), "steering.proposed from user: Do not edit generated files.") {
		t.Fatalf("user steering missing from next inference context: %s", joined.String())
	}
	if pending := o.drainActorEvents("session-1"); len(pending) != 0 {
		t.Fatalf("user steering was not acknowledged at the safe point: %+v", pending)
	}
}

func TestAppendActorEventsDrainsInboxOnce(t *testing.T) {
	o := &Orchestrator{
		stateDir: t.TempDir(),
		active:   map[string]*activeRun{"session-1": {id: 1, cancel: func() {}}},
	}
	if err := o.UserSteering(context.Background(), "session-1", "banguninfo ada di /apps/profile/BangunInfo"); err != nil {
		t.Fatal(err)
	}
	msgs, applied := o.appendActorEvents(nil, "session-1")
	if !applied || len(msgs) != 1 {
		t.Fatalf("applied=%v messages=%+v", applied, msgs)
	}
	if !strings.Contains(msgs[0].Content, "BangunInfo") {
		t.Fatalf("content = %q", msgs[0].Content)
	}
	if again, reapplied := o.appendActorEvents(nil, "session-1"); reapplied || len(again) != 0 {
		t.Fatalf("expected empty drain, got applied=%v messages=%+v", reapplied, again)
	}
}

func TestUserSteeringRejectedWhenIdle(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir(), active: make(map[string]*activeRun)}
	err := o.UserSteering(context.Background(), "session-1", "change course")
	if err == nil || !strings.Contains(err.Error(), "no active foreground generation") {
		t.Fatalf("error = %v, want idle-generation rejection", err)
	}
	if events := o.drainActorEvents("session-1"); len(events) != 0 {
		t.Fatalf("idle steering must not be queued: %+v", events)
	}
}

func TestUserSteeringRejectsInvalidOrCancelledInput(t *testing.T) {
	o := &Orchestrator{
		stateDir: t.TempDir(),
		active:   map[string]*activeRun{"session-1": {id: 1, cancel: func() {}}},
	}
	tests := []struct {
		name      string
		ctx       context.Context
		sessionID string
		message   string
		want      string
	}{
		{name: "empty session", ctx: context.Background(), message: "change", want: "session_id is required"},
		{name: "empty message", ctx: context.Background(), sessionID: "session-1", message: "  ", want: "steering message is required"},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests = append(tests, struct {
		name      string
		ctx       context.Context
		sessionID string
		message   string
		want      string
	}{name: "cancelled", ctx: cancelled, sessionID: "session-1", message: "change", want: context.Canceled.Error()})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := o.UserSteering(tt.ctx, tt.sessionID, tt.message)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
	if events := o.drainActorEvents("session-1"); len(events) != 0 {
		t.Fatalf("invalid steering must not be queued: %+v", events)
	}
	if !errors.Is(cancelled.Err(), context.Canceled) {
		t.Fatal("cancelled test context was not cancelled")
	}
}

func TestUserSteeringInterruptsBridgeStream(t *testing.T) {
	fake := &interruptibleBridge{started: make(chan struct{}, 1)}
	o := &Orchestrator{
		stateDir: t.TempDir(),
		vision:   make(map[string]bool),
		active:   map[string]*activeRun{"session-1": {id: 1, cancel: func() {}}},
	}
	out := make(chan bridge.StreamEvent, 32)
	statuses := make(chan string, 8)
	go func() {
		for ev := range out {
			if ev.Kind == bridge.EventStatus && ev.Status != "" {
				statuses <- ev.Status
			}
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := o.runTurnLoop(context.Background(), providerSnapshot{
			cfg:   config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
			entry: config.LLMBridge{Key: "test", Model: "model"},
			br:    fake,
		}, "task", []bridge.Message{{Role: "user", Content: "implement"}}, turnConfig{
			sessionID: "session-1",
			runID:     "session-1",
			sink:      chatSink{o: o, out: out},
			dispatch: func(_ context.Context, call parse.ToolCall) turnOutcome {
				if call.Name == "sapaloq_stop" {
					return turnOutcome{text: "Stopped", handled: true, stop: true}
				}
				return turnOutcome{}
			},
		})
		done <- err
	}()

	select {
	case <-fake.started:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge stream did not start")
	}
	if err := o.UserSteering(context.Background(), "session-1", "Ignore logs/qa-*"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case status := <-statuses:
			if status == "steering applied" {
				goto steeringApplied
			}
		case <-deadline:
			t.Fatal("steering was not applied during bridge stream")
		}
	}
steeringApplied:
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("turn loop did not finish after steering interrupt")
	}
	close(out)
	if fake.calls < 2 {
		t.Fatalf("Complete calls = %d, want at least 2 after steering interrupt", fake.calls)
	}
	var joined strings.Builder
	for _, message := range fake.requests[len(fake.requests)-1].Messages {
		joined.WriteString(message.Content)
		joined.WriteByte('\n')
	}
	if !strings.Contains(joined.String(), "Ignore logs/qa-*") {
		t.Fatalf("steering missing from follow-up inference: %s", joined.String())
	}
	if pending := o.drainActorEvents("session-1"); len(pending) != 0 {
		t.Fatalf("steering inbox not drained: %+v", pending)
	}
}

func TestSteeringSkippedWhenRunStopsWithLateQueue(t *testing.T) {
	o := &Orchestrator{
		stateDir: t.TempDir(),
		vision:   make(map[string]bool),
		active:   map[string]*activeRun{"session-1": {id: 1, cancel: func() {}}},
	}
	out := make(chan bridge.StreamEvent, 16)
	statuses := make(chan string, 4)
	go func() {
		for ev := range out {
			if ev.Kind == bridge.EventStatus {
				statuses <- ev.Status
			}
		}
	}()
	_, err := o.runTurnLoop(context.Background(), providerSnapshot{
		cfg:   config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    &lateSteeringStopBridge{},
	}, "task", []bridge.Message{{Role: "user", Content: "implement"}}, turnConfig{
		sessionID: "session-1",
		runID:     "session-1",
		sink:      chatSink{o: o, out: out},
		dispatch: func(ctx context.Context, call parse.ToolCall) turnOutcome {
			if call.Name == "sapaloq_stop" {
				_ = o.UserSteering(ctx, "session-1", "too late")
				return turnOutcome{text: "Stopped", handled: true, stop: true}
			}
			return turnOutcome{}
		},
	})
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case status := <-statuses:
			if status == "steering skipped - run ended" {
				if pending := o.drainActorEvents("session-1"); len(pending) != 0 {
					t.Fatalf("inbox not drained after skip: %+v", pending)
				}
				return
			}
		case <-deadline:
			t.Fatal("steering skipped status was not emitted")
		}
	}
}

type interruptibleBridge struct {
	started  chan struct{}
	calls    int
	requests []bridge.Request
}

func (b *interruptibleBridge) ID() string              { return "interruptible" }
func (b *interruptibleBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *interruptibleBridge) Complete(ctx context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.calls++
	b.requests = append(b.requests, req)
	if b.started == nil {
		b.started = make(chan struct{}, 1)
	}
	select {
	case b.started <- struct{}{}:
	default:
	}
	out := make(chan bridge.StreamEvent, 4)
	if b.calls == 1 {
		go func() {
			out <- bridge.StreamEvent{Kind: bridge.EventThinkingDelta, Delta: "thinking"}
			<-ctx.Done()
			out <- bridge.StreamEvent{Kind: bridge.EventDone}
			close(out)
		}()
		return out, nil
	}
	go func() {
		out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "ack"}
		stop := parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`)}
		out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &stop}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
		close(out)
	}()
	return out, nil
}

type lateSteeringStopBridge struct{}

func (b *lateSteeringStopBridge) ID() string              { return "late-stop" }
func (b *lateSteeringStopBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *lateSteeringStopBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		stop := parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`)}
		out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &stop}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
		close(out)
	}()
	return out, nil
}
