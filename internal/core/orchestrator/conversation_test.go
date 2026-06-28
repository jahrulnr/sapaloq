package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
	"github.com/jahrulnr/sapaloq/internal/parse/artifacts"
)

// TestLooksLikeTransientTransport4xx pins the regression that motivated this
// classifier: a provider returning 401/403/404 (auth / model not found /
// forbidden) must NOT be retried. Otherwise a wrong API key or a non-existent
// model name burns the full retry budget before the user gets a chance to
// read the error, and the agent appears to hang for 4 attempts.
//
// The first bug surfaced in chat-1782224023561155198: provider blackbox
// returned 404 for model "blackboxai/anthropic/claude-opus-4.8" but the
// orchestrator retried 4 times (mixing transient transport errors with
// deterministic 4xx), so the widget stayed in "submitting" state for ~7
// minutes and the user could not stop it from the UI.
func TestLooksLikeTransientTransport4xx(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
		why  string
	}{
		// The real production errors from the stuck chat above. Both must
		// NOT be classified as transient (the agent should fail fast).
		{
			msg:  `provider-bridge: upstream status 401: {"error":{"message":"blackbox.Error: AuthenticationError: Vercel_ai_gatewayException - Authentication failed. Create an API key and set in AI_GATEWAY_API_KEY environment variable","code":"401"}}`,
			want: false,
			why:  "401 AuthenticationError must fail fast",
		},
		{
			msg:  `provider-bridge: upstream status 404: {"error":{"message":"blackbox.Error: NotFoundError: Vercel_ai_gatewayException - Not Found","code":"404"}}`,
			want: false,
			why:  "404 NotFoundError (model missing) must fail fast",
		},

		// Other 4xx with deterministic causes - same treatment.
		{"provider-bridge: upstream status 403: forbidden", false, "403 forbidden is deterministic"},
		{"provider-bridge: upstream status 400: invalid request", false, "400 invalid request is deterministic"},
		{"provider-bridge: upstream status 400: maximum context length exceeded", false, "context overflow is its own path (compaction), not transient retry"},
		{"provider-bridge: upstream status 400: rate limit exceeded", false, "rate limit is deterministic on this turn (cannot recover by retrying the same request)"},
		{"provider-bridge: SSE idle timeout: no data from upstream", true, "SSE idle IS transient (real flaky network/upstream hang)"},
		{"connection reset by peer", true, "transport-level reset is transient"},
		{"provider-bridge: upstream status 502: bad gateway", true, "5xx IS transient"},
		{"provider-bridge: upstream status 503: service unavailable", true, "5xx IS transient"},
		{"provider-bridge: upstream status 504: gateway timeout", true, "5xx IS transient"},
		{"provider-bridge: upstream status 429: too many requests", true, "429 IS transient (with backoff)"},
		{"EOF", true, "premature EOF is transient"},
		{"i/o timeout", true, "I/O timeout is transient"},
		{"cursor node stream returned empty response", true, "empty api2 turn is retried before failing"},
		{"cursor chat stream returned no data", true, "empty chat stream is retried before failing"},
	}
	for _, tc := range cases {
		got := looksLikeTransientTransport(tc.msg)
		if got != tc.want {
			t.Errorf("looksLikeTransientTransport(%q) = %v, want %v (%s)", tc.msg, got, tc.want, tc.why)
		}
	}
}

type sequenceBridge struct {
	requests []bridge.Request
}

func (b *sequenceBridge) ID() string              { return "sequence" }
func (b *sequenceBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *sequenceBridge) Complete(_ context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.requests = append(b.requests, req)
	out := make(chan bridge.StreamEvent, 4)
	call := len(b.requests)
	go func() {
		defer close(out)
		if call == 1 {
			args, _ := json.Marshal(map[string]string{"task_id": "task-test"})
			tool := parse.ToolCall{Name: "sapaloq_get_task_status", Arguments: args}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &tool}
		} else {
			// Final turn: answer, then signal completion the only way a run
			// can now end - an explicit terminal tool (no more "tool-less =
			// stop"). The stop call rides on the same turn as the answer.
			out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "continued"}
			stop := parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`)}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &stop}
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// thinkingBridge emits one reasoning delta then a response, with no tool calls.
type thinkingBridge struct{}

func (b *thinkingBridge) ID() string              { return "thinking" }
func (b *thinkingBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *thinkingBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		out <- bridge.StreamEvent{Kind: bridge.EventThinkingDelta, Delta: "let me reason "}
		out <- bridge.StreamEvent{Kind: bridge.EventThinkingDelta, Delta: "about this"}
		out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "final answer"}
		stop := parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`)}
		out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &stop}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// stuckBridge models a provider/bridge that ignores context cancellation and
// never closes its event channel. User Stop must still finish the conversation
// immediately instead of waiting for this producer forever.
type stuckBridge struct {
	started chan struct{}
}

func (b *stuckBridge) ID() string              { return "stuck" }
func (b *stuckBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *stuckBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	close(b.started)
	return make(chan bridge.StreamEvent), nil
}

func TestRunConversationCancellationDoesNotWaitForBridgeClose(t *testing.T) {
	fake := &stuckBridge{started: make(chan struct{})}
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "test", Model: "model"}, br: fake}
	out := make(chan bridge.StreamEvent, 16)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := orch.runConversation(ctx, snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "hi"}}, nil)
		done <- err
	}()

	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("bridge did not start")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled conversation returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancelled conversation waited for the bridge channel to close")
	}
}

type retryWithoutCloseBridge struct {
	calls int
}

func (b *retryWithoutCloseBridge) ID() string              { return "retry-without-close" }
func (b *retryWithoutCloseBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *retryWithoutCloseBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.calls++
	out := make(chan bridge.StreamEvent, 4)
	if b.calls == 1 {
		out <- bridge.StreamEvent{Kind: bridge.EventError, Error: "provider-bridge: upstream status 500: unavailable"}
		return out, nil
	}
	out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "recovered"}
	stop := parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`)}
	out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &stop}
	out <- bridge.StreamEvent{Kind: bridge.EventDone}
	return out, nil
}

func TestRunConversationRetryDoesNotDrainBrokenStream(t *testing.T) {
	orig := transportRetryBaseBackoff
	transportRetryBaseBackoff = 0
	defer func() { transportRetryBaseBackoff = orig }()

	fake := &retryWithoutCloseBridge{}
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "test", Model: "model"}, br: fake}
	out := make(chan bridge.StreamEvent, 16)

	result, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.String() != "recovered" {
		t.Fatalf("result = %q, want recovered", result.String())
	}
	if fake.calls != 2 {
		t.Fatalf("calls = %d, want one retry", fake.calls)
	}
}

func TestRunConversationCapturesThinking(t *testing.T) {
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "test", Model: "model"}, br: &thinkingBridge{}}
	out := make(chan bridge.StreamEvent, 16)
	go func() {
		for range out {
		}
	}()
	var thinking strings.Builder
	result, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "hi"}}, &thinking)
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	if result.String() != "final answer" {
		t.Fatalf("answer = %q, want %q", result.String(), "final answer")
	}
	if thinking.String() != "let me reason about this" {
		t.Fatalf("thinking = %q, want accumulated reasoning", thinking.String())
	}
}

func TestRunConversationContinuesAfterToolResult(t *testing.T) {
	fake := &sequenceBridge{}
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	if err := orch.writeTask(taskRecord{ID: "task-test", Status: "done", Result: "result"}); err != nil {
		t.Fatal(err)
	}
	snap := providerSnapshot{
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}
	out := make(chan bridge.StreamEvent, 16)
	go func() {
		for range out {
		}
	}()
	result, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "status"}}, nil)
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(fake.requests))
	}
	if result.String() != "continued" {
		t.Fatalf("result = %q", result.String())
	}
	if got := fake.requests[1].Messages[len(fake.requests[1].Messages)-1].Content; got == "" {
		t.Fatal("tool result was not sent back to model")
	}
}

// TestRunConversationFeedsToolResultsAsPureData verifies the continuation
// message sent back to the model is PURE DATA: the tool result wrapped in
// <untrusted_data> tags, with none of the old steering (observe/summarize/
// continue) or usage-readout text. All that steering now lives in the persona
// system prompt; the tool turn stays clean so the model reasons over it best.
func TestRunConversationFeedsToolResultsAsPureData(t *testing.T) {
	fake := &sequenceBridge{}
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	if err := orch.writeTask(taskRecord{ID: "task-test", Status: "done", Result: "result"}); err != nil {
		t.Fatal(err)
	}
	snap := providerSnapshot{
		cfg:   config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}
	out := make(chan bridge.StreamEvent, 16)
	go func() {
		for range out {
		}
	}()
	_, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "status"}}, nil)
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	// The 2nd request's last message is the continuation we built after turn 1
	// (which made exactly one tool call).
	got := fake.requests[1].Messages[len(fake.requests[1].Messages)-1].Content
	// Pure data: wrapped in <untrusted_data>, carrying the tool result.
	if !strings.Contains(got, "<untrusted_data>") || !strings.Contains(got, "</untrusted_data>") {
		t.Fatalf("continuation should wrap tool results in <untrusted_data>: %q", got)
	}
	// No steering / usage-readout text should ride along anymore.
	for _, banned := range []string{"Usage", "tool-calls so far", "Tool output observed", "Continue the original request"} {
		if strings.Contains(got, banned) {
			t.Fatalf("continuation should not contain steering text %q: %q", banned, got)
		}
	}
}

type longSequenceBridge struct {
	requests int
	tools    int
}

func (b *longSequenceBridge) ID() string              { return "long-sequence" }
func (b *longSequenceBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *longSequenceBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.requests++
	call := b.requests
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		if call <= b.tools {
			args, _ := json.Marshal(map[string]string{"task_id": fmt.Sprintf("task-%d", call)})
			tool := parse.ToolCall{Name: "sapaloq_get_task_status", Arguments: args}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &tool}
		} else {
			out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "finished"}
			stop := parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`)}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &stop}
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

func TestRunConversationSupportsMoreThanEightContinuations(t *testing.T) {
	fake := &longSequenceBridge{tools: 12}
	orch := &Orchestrator{memoryDir: t.TempDir(), vision: make(map[string]bool)}
	for i := 1; i <= fake.tools; i++ {
		if err := orch.writeTask(taskRecord{ID: fmt.Sprintf("task-%d", i), Status: "done", Result: fmt.Sprintf("result-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	snap := providerSnapshot{
		cfg:   config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}
	out := make(chan bridge.StreamEvent, 128)
	go func() {
		for range out {
		}
	}()
	result, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "run"}}, nil)
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	if fake.requests != 13 {
		t.Fatalf("requests = %d, want 13", fake.requests)
	}
	if result.String() != "finished" {
		t.Fatalf("result = %q", result.String())
	}
}

func TestRunConversationStopsIdenticalToolLoop(t *testing.T) {
	orch := &Orchestrator{memoryDir: t.TempDir(), vision: make(map[string]bool)}
	if err := orch.writeTask(taskRecord{ID: "task-1", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	repeating := &repeatingToolBridge{}
	snap := providerSnapshot{
		cfg:   config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    repeating,
	}
	out := make(chan bridge.StreamEvent, 64)
	go func() {
		for range out {
		}
	}()
	_, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "loop"}}, nil)
	close(out)
	if err == nil || !strings.Contains(err.Error(), "identical tool call") {
		t.Fatalf("err = %v", err)
	}
}

// TestDisabledIdenticalToolGuardLetsLoopRun proves a negative
// MaxIdenticalToolCalls disables the identical-tool loop guard: the same
// repeating bridge that trips the guard above now runs until the (still
// enforced) MaxToolCalls resource cap instead. This is the "observe raw model
// behavior" escape hatch - the loop-breaker is off, but real resource caps
// still bound the run.
func TestDisabledIdenticalToolGuardLetsLoopRun(t *testing.T) {
	orch := &Orchestrator{memoryDir: t.TempDir(), vision: make(map[string]bool)}
	if err := orch.writeTask(taskRecord{ID: "task-1", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	oc := config.DefaultOrchestratorConfig()
	oc.Continuation.MaxIdenticalToolCalls = -1 // explicitly disabled
	oc.Continuation.MaxToolCalls = 3           // resource cap still bounds the run
	oc = oc.WithDefaults()                     // must NOT resurrect the -1
	if oc.Continuation.MaxIdenticalToolCalls != -1 {
		t.Fatalf("WithDefaults resurrected disabled guard: %d", oc.Continuation.MaxIdenticalToolCalls)
	}
	snap := providerSnapshot{
		cfg:   config.Config{Orchestrator: oc},
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    &repeatingToolBridge{},
	}
	out := make(chan bridge.StreamEvent, 64)
	go func() {
		for range out {
		}
	}()
	_, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "loop"}}, nil)
	close(out)
	if err == nil || !strings.Contains(err.Error(), "tool-call budget") {
		t.Fatalf("expected tool-call budget stop (guard disabled), got err = %v", err)
	}
}

type repeatingToolBridge struct{}

func (b *repeatingToolBridge) ID() string              { return "repeat" }
func (b *repeatingToolBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *repeatingToolBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent, 2)
	go func() {
		defer close(out)
		args, _ := json.Marshal(map[string]string{"task_id": "task-1"})
		tool := parse.ToolCall{Name: "sapaloq_get_task_status", Arguments: args}
		out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &tool}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// TestRunTurnLoopUnlimitedTurnsStillBoundedByGuards proves the executor's
// unlimited turn budget (maxInferenceTurns < 0) does NOT mean "loop forever":
// a misbehaving model that repeats an identical tool call is still stopped by
// the identical-tool guard, not by a turn ceiling. This is the safety
// contract that lets the executor run without an arbitrary turn cap.
func TestRunTurnLoopUnlimitedTurnsStillBoundedByGuards(t *testing.T) {
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		cfg:       config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		vision:    make(map[string]bool),
	}
	out := make(chan bridge.StreamEvent, 64)
	go func() {
		for range out {
		}
	}()
	cfg := turnConfig{
		sessionID:         "s1",
		runID:             "actor-unlimited",
		tools:             []string{"sapaloq_get_task_status"},
		sink:              chatSink{o: o, out: out},
		maxInferenceTurns: unlimitedTurnsBudget, // < 0 → no turn ceiling
		dispatch: func(context.Context, parse.ToolCall) turnOutcome {
			return turnOutcome{text: "status", handled: true}
		},
	}
	_, err := o.runTurnLoop(context.Background(), providerSnapshot{
		cfg:   o.cfg,
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    &repeatingToolBridge{},
	}, "task", []bridge.Message{{Role: "user", Content: "loop"}}, cfg)
	close(out)
	if err == nil || !strings.Contains(err.Error(), "identical tool call") {
		t.Fatalf("unlimited turns must still be bounded by the identical-tool guard; err = %v", err)
	}
}

func TestWaitForTaskChangeUsesBackendSignal(t *testing.T) {
	orch := &Orchestrator{memoryDir: t.TempDir()}
	now := time.Now().UTC()
	if err := orch.writeTask(taskRecord{ID: "task-wait", Status: "in_progress", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(25 * time.Millisecond)
		_ = orch.writeTask(taskRecord{ID: "task-wait", Status: "done", Result: "ok", CreatedAt: now, UpdatedAt: time.Now().UTC()})
	}()
	record, changed, err := orch.waitForTaskChange(context.Background(), "task-wait", 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || record.Status != "done" {
		t.Fatalf("changed=%v record=%#v", changed, record)
	}
}

// TestWaitIgnoresNonTerminalProgress proves the "blocking progress" fix: a bare
// progress update (UpdatedAt advances, status stays in_progress - e.g. the agent
// calling sapaloq_update_task_progress) must NOT break the wait. Otherwise the
// orchestrator returns "changed to in_progress", re-waits, and the chat freezes
// in a wait→progress→wait loop. The wait should run out its (short) window and
// report no meaningful change.
func TestWaitIgnoresNonTerminalProgress(t *testing.T) {
	orch := &Orchestrator{memoryDir: t.TempDir()}
	now := time.Now().UTC()
	if err := orch.writeTask(taskRecord{ID: "task-prog", Status: "in_progress", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Emit several progress bumps with the SAME status during the wait window.
	go func() {
		for i := 0; i < 3; i++ {
			time.Sleep(15 * time.Millisecond)
			_ = orch.writeTask(taskRecord{ID: "task-prog", Status: "in_progress", CreatedAt: now, UpdatedAt: time.Now().UTC()})
		}
	}()
	record, changed, err := orch.waitForTaskChange(context.Background(), "task-prog", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("wait broke on a non-terminal progress update; want changed=false (record=%#v)", record)
	}
	if record.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", record.Status)
	}
}

// TestWaitReturnsOnStatusTransition confirms a genuine non-terminal status
// transition (pending → in_progress) still ends the wait promptly.
func TestWaitReturnsOnStatusTransition(t *testing.T) {
	orch := &Orchestrator{memoryDir: t.TempDir()}
	now := time.Now().UTC()
	if err := orch.writeTask(taskRecord{ID: "task-trans", Status: "pending", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(25 * time.Millisecond)
		_ = orch.writeTask(taskRecord{ID: "task-trans", Status: "in_progress", CreatedAt: now, UpdatedAt: time.Now().UTC()})
	}()
	record, changed, err := orch.waitForTaskChange(context.Background(), "task-trans", 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || record.Status != "in_progress" {
		t.Fatalf("status transition did not end the wait: changed=%v record=%#v", changed, record)
	}
}

func TestCompactConversationPreservesGoalAndRecentMessages(t *testing.T) {
	messages := []bridge.Message{{Role: "system", Content: "system"}}
	for i := 0; i < 12; i++ {
		messages = append(messages, bridge.Message{Role: "user", Content: fmt.Sprintf("message-%d", i)})
	}
	compacted := compactConversationMessages(messages, "original goal", 0.30)
	if len(compacted) >= len(messages) {
		t.Fatalf("compaction did not reduce messages: %d >= %d", len(compacted), len(messages))
	}
	if !strings.Contains(compacted[1].Content, "original goal") {
		t.Fatalf("checkpoint does not preserve goal: %q", compacted[1].Content)
	}
	if compacted[len(compacted)-1].Content != "message-11" {
		t.Fatalf("latest message not preserved: %#v", compacted[len(compacted)-1])
	}
}

func TestExtractImagesBuildsVisionPayload(t *testing.T) {
	messages, images := extractImages([]bridge.Message{{
		Role:    "user",
		Content: "describe\n![sample](data:image/png;base64,aGVsbG8=)",
	}})
	if len(images) != 1 || images[0].MimeType != "image/png" {
		t.Fatalf("images = %#v", images)
	}
	if messages[0].Content == "" || messages[0].Content == "describe\n![sample](data:image/png;base64,aGVsbG8=)" {
		t.Fatalf("image marker was not replaced: %q", messages[0].Content)
	}
}

func TestCalledToolsNote(t *testing.T) {
	mk := func(names ...string) []scheduledTool {
		tools := make([]scheduledTool, 0, len(names))
		for _, n := range names {
			tools = append(tools, scheduledTool{call: parse.ToolCall{Name: n}})
		}
		return tools
	}
	cases := []struct {
		name  string
		tools []scheduledTool
		want  string
	}{
		{"none", nil, ""},
		{"single", mk("sapaloq_spawn_agent"), "[Called tools: sapaloq_spawn_agent]"},
		{"multiple distinct", mk("read_file", "exec"), "[Called tools: read_file, exec]"},
		{"duplicates collapse", mk("sapaloq_spawn_agent", "sapaloq_spawn_agent"), "[Called tools: sapaloq_spawn_agent ×2]"},
		{"mixed", mk("exec", "read_file", "exec"), "[Called tools: exec ×2, read_file]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := calledToolsNote(tc.tools); got != tc.want {
				t.Fatalf("calledToolsNote = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunConversationRecordsToolCallInTranscript proves the double-spawn fix:
// after a turn that invoked a tool, the assistant message replayed to the model
// on the next turn must carry an explicit [Called tools: …] record. Without it
// the transcript shows only the model's narration plus a tool result, with no
// proof the model itself called the tool - which leads some models (e.g. Opus)
// to second-guess ("I forgot to call it") and re-issue the same call.
func TestRunConversationRecordsToolCallInTranscript(t *testing.T) {
	fake := &sequenceBridge{}
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	if err := orch.writeTask(taskRecord{ID: "task-test", Status: "done", Result: "result"}); err != nil {
		t.Fatal(err)
	}
	snap := providerSnapshot{
		cfg:   config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}
	out := make(chan bridge.StreamEvent, 16)
	go func() {
		for range out {
		}
	}()
	_, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "status"}}, nil)
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(fake.requests))
	}
	// The 2nd request replays the turn-1 continuation. The appended assistant
	// message (second-to-last) must record the tool the model just called.
	msgs := fake.requests[1].Messages
	var recorded bool
	for _, m := range msgs {
		if m.Role == "assistant" && strings.Contains(m.Content, "Called tools: sapaloq_get_task_status") {
			recorded = true
			break
		}
	}
	if !recorded {
		t.Fatalf("assistant transcript missing [Called tools: …] record; messages=%#v", msgs)
	}
}

// busyBridge keeps producing deltas - one short delay then output, repeated -
// so the run is always making progress. Total runtime exceeds the idle window,
// but no single gap does. It finishes (tool-less done) after `turns` turns.
type busyBridge struct {
	gap   time.Duration
	turns int
	seen  int
}

func (b *busyBridge) ID() string              { return "busy" }
func (b *busyBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *busyBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.seen++
	last := b.seen >= b.turns
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		// A couple of progress deltas with a sub-window gap between them.
		for i := 0; i < 2; i++ {
			time.Sleep(b.gap)
			out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "x"}
		}
		_ = last
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// silentBridge opens a stream and never emits anything - a stuck network /
// dead stream. The idle deadline must cancel it.
type silentBridge struct{}

func (b *silentBridge) ID() string              { return "silent" }
func (b *silentBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *silentBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent)
	// Never write, never close - only ctx cancellation (the idle deadline)
	// unblocks the loop's select on the stream.
	return out, nil
}

func shrinkIdleWindow(t *testing.T, d time.Duration) {
	t.Helper()
	prev := idleWindowUnit
	idleWindowUnit = d
	t.Cleanup(func() { idleWindowUnit = prev })
}

func newIdleTestOrch(t *testing.T) *Orchestrator {
	t.Helper()
	cfg := config.DefaultOrchestratorConfig()
	cfg.Continuation.MaxWallTimeMinutes = 1 // × idleWindowUnit (shrunk in tests)
	return &Orchestrator{
		memoryDir: t.TempDir(),
		cfg:       config.Config{Orchestrator: cfg},
		vision:    make(map[string]bool),
	}
}

// TestWallTimeIsIdleNotTotal proves a busy agent is NOT killed for total
// runtime: with a 20ms idle window it produces progress every ~8ms across
// several turns (well over 20ms total) and still finishes cleanly.
func TestWallTimeIsIdleNotTotal(t *testing.T) {
	shrinkIdleWindow(t, 20*time.Millisecond)
	o := newIdleTestOrch(t)
	out := make(chan bridge.StreamEvent, 128)
	go func() {
		for range out {
		}
	}()
	cfg := turnConfig{
		sessionID: "s1",
		runID:     "actor-busy",
		tools:     []string{},
		sink:      chatSink{o: o, out: out},
	}
	done := make(chan error, 1)
	go func() {
		_, err := o.runTurnLoop(context.Background(), providerSnapshot{
			cfg:   o.cfg,
			entry: config.LLMBridge{Key: "test", Model: "model"},
			br:    &busyBridge{gap: 8 * time.Millisecond, turns: 4},
		}, "task", []bridge.Message{{Role: "user", Content: "go"}}, cfg)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("busy agent must not be idle-cancelled: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("busy run did not finish")
	}
	close(out)
}

// TestWallTimeCancelsStalledRun proves a silent (stuck) run IS cancelled once
// the idle window elapses with no activity.
func TestWallTimeCancelsStalledRun(t *testing.T) {
	shrinkIdleWindow(t, 30*time.Millisecond)
	o := newIdleTestOrch(t)
	out := make(chan bridge.StreamEvent, 16)
	go func() {
		for range out {
		}
	}()
	cfg := turnConfig{
		sessionID: "s1",
		runID:     "actor-stuck",
		tools:     []string{},
		sink:      chatSink{o: o, out: out},
	}
	done := make(chan error, 1)
	go func() {
		_, err := o.runTurnLoop(context.Background(), providerSnapshot{
			cfg:   o.cfg,
			entry: config.LLMBridge{Key: "test", Model: "model"},
			br:    &silentBridge{},
		}, "task", []bridge.Message{{Role: "user", Content: "go"}}, cfg)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stalled") {
			t.Fatalf("stalled run should be idle-cancelled; err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled run was not cancelled by idle deadline")
	}
	close(out)
}

// malformedToolBridge models a non-native model (e.g. MiniMax-M3) that emits a
// tool call which produces no usable result (an unhandled tool name → empty
// toolResults) on its first turn, then answers in plain text once nudged.
// Without the malformed-tool-call recovery this would `done` after turn 1 with
// no answer; with it the run nudges and continues to the real answer.
type malformedToolBridge struct {
	calls    int
	requests []bridge.Request
}

func (b *malformedToolBridge) ID() string              { return "malformed-tool" }
func (b *malformedToolBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *malformedToolBridge) Complete(_ context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.requests = append(b.requests, req)
	b.calls++
	out := make(chan bridge.StreamEvent, 4)
	call := b.calls
	go func() {
		defer close(out)
		if call == 1 {
			// An unknown tool name is not handled by dispatchTool,
			// so it yields no toolResult - exactly the "tool emitted but nothing
			// executed" shape a mangled inline batch produces.
			tool := parse.ToolCall{Name: "definitely_not_a_real_tool", Arguments: []byte(`{}`), Source: "openai_inline"}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &tool}
		} else {
			out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "here is the answer"}
			stop := parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`)}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &stop}
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

func TestRunConversationRecoversFromMalformedToolCall(t *testing.T) {
	fake := &malformedToolBridge{}
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "test", Model: "model"}, br: fake}
	out := make(chan bridge.StreamEvent, 32)
	go func() {
		for range out {
		}
	}()
	result, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "hi"}}, nil)
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	if result.String() != "here is the answer" {
		t.Fatalf("result = %q, want the recovered answer", result.String())
	}
	if fake.calls != 2 {
		t.Fatalf("calls = %d, want a nudge + retry (2)", fake.calls)
	}
	// The nudge must have been appended as a user message before the retry.
	last := fake.requests[1].Messages[len(fake.requests[1].Messages)-1].Content
	if !strings.Contains(last, "well-formed call") {
		t.Fatalf("retry prompt missing malformed-tool nudge: %q", last)
	}
}

// alwaysMalformedToolBridge never produces a usable tool result; it must still
// terminate (bounded by maxMalformedToolTurns) instead of looping forever.
type alwaysMalformedToolBridge struct {
	calls int
}

func (b *alwaysMalformedToolBridge) ID() string              { return "always-malformed" }
func (b *alwaysMalformedToolBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *alwaysMalformedToolBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.calls++
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		tool := parse.ToolCall{Name: "definitely_not_a_real_tool", Arguments: []byte(`{}`), Source: "openai_inline"}
		out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &tool}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

func TestRunConversationMalformedToolCallIsBounded(t *testing.T) {
	fake := &alwaysMalformedToolBridge{}
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "test", Model: "model"}, br: fake}
	out := make(chan bridge.StreamEvent, 64)
	go func() {
		for range out {
		}
	}()
	done := make(chan struct{})
	go func() {
		_, _ = orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "hi"}}, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("malformed-tool recovery did not terminate")
	}
	close(out)
	// 1 initial turn + up to maxMalformedToolTurns nudged retries, then it
	// finishes (a clean tool-less done once the guard is exhausted).
	if fake.calls < 2 || fake.calls > 6 {
		t.Fatalf("calls = %d, want bounded retries", fake.calls)
	}
}

// repeatingTextBridge always replies with the SAME tool-less text and never
// calls a terminal tool. Under the new "stop only via terminal tool" model the
// run must NOT loop forever: the no-progress finish ends it CLEANLY (no error),
// which is the common case of a model that answers and never says stop.
type repeatingTextBridge struct {
	calls    int
	requests []bridge.Request
}

func (b *repeatingTextBridge) ID() string              { return "repeating-text" }
func (b *repeatingTextBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *repeatingTextBridge) Complete(_ context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.requests = append(b.requests, req)
	b.calls++
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "Hai! Ada yang bisa kubantu?"}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// TestToolLessTurnNeverFinishesOnAbsenceOfTool proves the polarity flip: a
// tool-less turn does NOT end the run by itself (no "no-tool = stop", no NO_OP
// sentinel). The run instead continues until the toolless-turn budget closes
// it cleanly once the model just repeats itself. It also proves the
// continuation fed back is the single content-blind nudge (mentions
// sapaloq_stop, never NO_OP) and is never derived from the model's text.
func TestToolLessTurnNeverFinishesOnAbsenceOfTool(t *testing.T) {
	fake := &repeatingTextBridge{}
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		cfg:       config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	out := make(chan bridge.StreamEvent, 128)
	go func() {
		for range out {
		}
	}()
	cfg := turnConfig{
		sessionID: "s1",
		runID:     "actor-toolless",
		tools:     []string{},
		sink:      chatSink{o: o, out: out},
	}
	done := make(chan error, 1)
	go func() {
		_, err := o.runTurnLoop(context.Background(), providerSnapshot{
			cfg:   o.cfg,
			entry: config.LLMBridge{Key: "test", Model: "model"},
			br:    fake,
		}, "task", []bridge.Message{{Role: "user", Content: "hai hai"}}, cfg)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("repeating tool-less run should finish cleanly via the toolless-turn budget: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tool-less run did not finish - it must be bounded by the toolless-turn budget")
	}
	close(out)
	// It must have continued past the first turn (a tool-less turn is NOT a
	// stop) and then been bounded - not run away.
	if fake.calls < 2 {
		t.Fatalf("calls = %d, want the run to continue past the first tool-less turn", fake.calls)
	}
	if fake.calls > config.DefaultOrchestratorConfig().Continuation.MaxNoProgressTurns+2 {
		t.Fatalf("calls = %d, want the toolless-turn budget to bound it tightly", fake.calls)
	}
	// The continuation fed back must be the single content-blind nudge: it
	// points at sapaloq_stop and must NOT resurrect the deleted NO_OP sentinel.
	last := fake.requests[len(fake.requests)-1].Messages
	cont := last[len(last)-1].Content
	if !strings.Contains(cont, "sapaloq_stop") {
		t.Fatalf("continuation should point at the terminal tool, got %q", cont)
	}
	// The continuation must also frame stopping as a silent action (no status
	// narration / sign-off) so the model stops calling the tool AND narrating.
	// Use a stable keyword ("silent"), not the full sentence, to stay robust to
	// minor wording tweaks.
	if !strings.Contains(cont, "silent") {
		t.Fatalf("continuation should frame stopping as a silent action, got %q", cont)
	}
	if strings.Contains(cont, "NO_OP") {
		t.Fatalf("continuation must not use the removed NO_OP sentinel, got %q", cont)
	}
	// The autopilot continuation is authored by SapaLOQ, not the human, so it
	// MUST be wrapped in the <sapaloq:autopilot> markers - that is the only
	// thing letting the model tell it apart from a real user turn (both ride
	// the wire "user" role).
	if !strings.Contains(cont, sapaloqControlOpen) || !strings.Contains(cont, sapaloqControlClose) {
		t.Fatalf("autopilot continuation must be wrapped in <sapaloq:autopilot>…</sapaloq:autopilot>, got %q", cont)
	}
	// The genuine human turn (the first message) must NOT carry the marker -
	// an unmarked user turn is precisely what identifies the real human.
	if got := last[0].Content; strings.Contains(got, sapaloqControlOpen) {
		t.Fatalf("the real user turn must stay unmarked, got %q", got)
	}
}

// TestForegroundAskAutoStopsAfterCleanToolLessReply proves foreground ask chat
// finishes after one sane tool-less reply instead of burning the autopilot loop.
func TestForegroundAskAutoStopsAfterCleanToolLessReply(t *testing.T) {
	fake := &repeatingTextBridge{}
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		cfg:       config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	out := make(chan bridge.StreamEvent, 128)
	go func() {
		for range out {
		}
	}()
	cfg := turnConfig{
		sessionID:     "s1",
		runID:         "actor-ask",
		tools:         []string{},
		sink:          chatSink{o: o, out: out},
		foregroundAsk: true,
	}
	_, err := o.runTurnLoop(context.Background(), providerSnapshot{
		cfg:   o.cfg,
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}, "task", []bridge.Message{{Role: "user", Content: "heyy"}}, cfg)
	if err != nil {
		t.Fatalf("foreground ask should finish cleanly: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d, want 1 clean tool-less reply to end the run", fake.calls)
	}
}

// TestForegroundAskDropsConfabulatedArtifact proves edit-session artifact dumps
// never persist as assistant turns on foreground ask chat.
func TestForegroundAskDropsConfabulatedArtifact(t *testing.T) {
	fake := &artifactDumpBridge{}
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		cfg:       config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	out := make(chan bridge.StreamEvent, 128)
	var deltas []string
	go func() {
		for ev := range out {
			if ev.Kind == bridge.EventResponseDelta {
				deltas = append(deltas, ev.Delta)
			}
		}
	}()
	cfg := turnConfig{
		sessionID:       "s-artifact",
		runID:           "actor-artifact",
		tools:           []string{},
		sink:            chatSink{o: o, out: out},
		recordToolTurns: true,
		foregroundAsk:   true,
	}
	result, err := o.runTurnLoop(context.Background(), providerSnapshot{
		cfg:   o.cfg,
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}, "heyy", []bridge.Message{{Role: "user", Content: "heyy"}}, cfg)
	if err != nil {
		t.Fatalf("artifact run should finish cleanly: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d, want a single turn before stop", fake.calls)
	}
	if got := strings.TrimSpace(result.String()); got != artifacts.FallbackAskNoiseRetry() {
		t.Fatalf("result = %q, want noise retry fallback", got)
	}
}

func TestForegroundAskFallbackOnThinkingOnlyPing(t *testing.T) {
	fake := &thinkingOnlyBridge{}
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		cfg:       config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	out := make(chan bridge.StreamEvent, 128)
	var deltas []string
	go func() {
		for ev := range out {
			if ev.Kind == bridge.EventResponseDelta {
				deltas = append(deltas, ev.Delta)
			}
		}
	}()
	cfg := turnConfig{
		sessionID:     "s-ping",
		runID:         "actor-ping",
		tools:         []string{},
		sink:          chatSink{o: o, out: out},
		foregroundAsk: true,
	}
	result, err := o.runTurnLoop(context.Background(), providerSnapshot{
		cfg:   o.cfg,
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}, "heyy", []bridge.Message{{Role: "user", Content: "heyy"}}, cfg)
	if err != nil {
		t.Fatalf("thinking-only ping should finish cleanly: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d, want 1 (no autopilot loop)", fake.calls)
	}
	if got := strings.TrimSpace(result.String()); got != artifacts.FallbackAskGreeting() {
		t.Fatalf("result = %q, want fallback greeting", got)
	}
}

func TestForegroundAskNoiseRetryOnThinkingOnlyTask(t *testing.T) {
	fake := &thinkingOnlyBridge{}
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		cfg:       config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	out := make(chan bridge.StreamEvent, 128)
	go func() {
		for range out {
		}
	}()
	cfg := turnConfig{
		sessionID:     "s-task",
		runID:         "actor-task",
		tools:         []string{},
		sink:          chatSink{o: o, out: out},
		foregroundAsk: true,
	}
	result, err := o.runTurnLoop(context.Background(), providerSnapshot{
		cfg:   o.cfg,
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}, "buat web keren di /tmp/profile", []bridge.Message{{Role: "user", Content: "buat web keren di /tmp/profile"}}, cfg)
	if err != nil {
		t.Fatalf("thinking-only task should finish with noise retry: %v", err)
	}
	if fake.calls != 4 {
		t.Fatalf("calls = %d, want 4 (3 noise retries + fallback)", fake.calls)
	}
	if got := strings.TrimSpace(result.String()); got != artifacts.FallbackAskNoiseRetry() {
		t.Fatalf("result = %q, want noise retry fallback", got)
	}
}

type thinkingOnlyBridge struct{ calls int }

func (b *thinkingOnlyBridge) ID() string              { return "thinking-only" }
func (b *thinkingOnlyBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{} }
func (b *thinkingOnlyBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.calls++
	out := make(chan bridge.StreamEvent, 8)
	go func() {
		defer close(out)
		out <- bridge.StreamEvent{
			Kind:  bridge.EventThinkingDelta,
			Delta: "The user wants me to troubleshoot Aether.\n\nThe user wants 16S rRNA analysis.\n",
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

type artifactDumpBridge struct{ calls int }

func (b *artifactDumpBridge) ID() string              { return "artifact" }
func (b *artifactDumpBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{} }
func (b *artifactDumpBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.calls++
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		out <- bridge.StreamEvent{
			Kind:  bridge.EventResponseDelta,
			Delta: "### Final file content: webapp/client/src/components/CommandPalette.jsx\nimport { useState } from 'react'\nexport function CommandPalette() {}\n",
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// stopToolBridge narrates for a couple of tool-less turns, then calls the
// terminal tool. Under the new model ONLY that terminal tool ends the run.
type stopToolBridge struct {
	calls   int
	stopAt  int
	stopped bool
}

func (b *stopToolBridge) ID() string              { return "stop-tool" }
func (b *stopToolBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *stopToolBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.calls++
	call := b.calls
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		if call >= b.stopAt {
			tool := parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`), Source: "openai"}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &tool}
		} else {
			// Vary the text so the no-progress finish does not close the run
			// first - we want the terminal tool to be what ends it.
			out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: fmt.Sprintf("working on step %d", call)}
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// TestRunFinishesOnTerminalTool proves the run keeps going through tool-less
// narration turns and ends exactly when the model calls the terminal tool.
func TestRunFinishesOnTerminalTool(t *testing.T) {
	fake := &stopToolBridge{stopAt: 3}
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		cfg:       config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	out := make(chan bridge.StreamEvent, 64)
	go func() {
		for range out {
		}
	}()
	cfg := turnConfig{
		sessionID: "s1",
		runID:     "actor-stop",
		tools:     []string{"sapaloq_stop"},
		sink:      chatSink{o: o, out: out},
		dispatch: func(_ context.Context, call parse.ToolCall) turnOutcome {
			if call.Name == "sapaloq_stop" {
				fake.stopped = true
				return turnOutcome{text: "Stopped: done", handled: true, stop: true}
			}
			return turnOutcome{}
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
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run should finish cleanly when the model calls the terminal tool: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not finish on the terminal tool")
	}
	close(out)
	if !fake.stopped {
		t.Fatal("terminal tool was never dispatched")
	}
	if fake.calls != 3 {
		t.Fatalf("calls = %d, want narrate(1) -> narrate(2) -> stop(3)", fake.calls)
	}
}

// TestRunEmitsTurnBoundaryBetweenTurns proves the orchestrator marks the seam
// between inference turns with EventTurnBoundary - the UI hint that lets the
// widget flush one bubble and start the next - and that it does NOT emit one
// after the terminal tool ends the run (the final turn needs no boundary). For
// stopAt:3 the sequence is narrate -> [boundary] -> narrate -> [boundary] ->
// stop, so exactly 2 boundaries are expected, each appearing before the next
// response_delta and never after EventDone.
func TestRunEmitsTurnBoundaryBetweenTurns(t *testing.T) {
	fake := &stopToolBridge{stopAt: 3}
	o := &Orchestrator{
		memoryDir: t.TempDir(),
		cfg:       config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	out := make(chan bridge.StreamEvent, 64)
	var (
		mu     sync.Mutex
		kinds  []bridge.EventKind
		drainW sync.WaitGroup
	)
	drainW.Add(1)
	go func() {
		defer drainW.Done()
		for ev := range out {
			mu.Lock()
			kinds = append(kinds, ev.Kind)
			mu.Unlock()
		}
	}()
	cfg := turnConfig{
		sessionID: "s1",
		runID:     "actor-boundary",
		tools:     []string{"sapaloq_stop"},
		sink:      chatSink{o: o, out: out},
		dispatch: func(_ context.Context, call parse.ToolCall) turnOutcome {
			if call.Name == "sapaloq_stop" {
				return turnOutcome{text: "Stopped: done", handled: true, stop: true}
			}
			return turnOutcome{}
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
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run should finish cleanly on the terminal tool: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not finish on the terminal tool")
	}
	close(out)
	drainW.Wait()

	mu.Lock()
	defer mu.Unlock()
	boundaries := 0
	lastDoneIdx := -1
	for i, k := range kinds {
		if k == bridge.EventTurnBoundary {
			boundaries++
		}
		if k == bridge.EventDone {
			lastDoneIdx = i
		}
	}
	if boundaries != 2 {
		t.Fatalf("turn_boundary count = %d, want 2 (after each of the two narration turns), kinds=%v", boundaries, kinds)
	}
	// No boundary may appear after the orchestrator's final EventDone - the
	// terminal turn must not be followed by a "start a new bubble" hint.
	for i := lastDoneIdx + 1; i >= 0 && i < len(kinds); i++ {
		if kinds[i] == bridge.EventTurnBoundary {
			t.Fatalf("turn_boundary emitted after final done at index %d, kinds=%v", i, kinds)
		}
	}
}

func TestEnsureConversationEndsWithUser(t *testing.T) {
	msgs := []bridge.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	out := ensureConversationEndsWithUser(msgs)
	if len(out) != len(msgs)+1 {
		t.Fatalf("expected user continuation, got %d messages", len(out))
	}
	if out[len(out)-1].Role != "user" {
		t.Fatalf("last role = %q, want user", out[len(out)-1].Role)
	}
	unchanged := ensureConversationEndsWithUser([]bridge.Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "mid"},
		{Role: "user", Content: "latest"},
	})
	if len(unchanged) != 3 {
		t.Fatalf("expected unchanged slice, got %d", len(unchanged))
	}
}
