package codex

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// replay drives the package's parser (scanStream + finalizeTerminal) over a
// JSONL transcript and returns all emitted events. This is the offline "golden
// replay" path: it exercises the exact mapping + terminal logic the live runner
// uses (runCodex calls the same two functions), with no codex CLI involved.
//
// abnormal is nil because there is no process behind a replay.
func replay(t *testing.T, jsonl string) []bridge.StreamEvent {
	t.Helper()
	return replayCtx(t, context.Background(), jsonl)
}

func replayCtx(t *testing.T, ctx context.Context, jsonl string) []bridge.StreamEvent {
	t.Helper()
	out := make(chan bridge.StreamEvent, 256)
	res, _ := scanStream(ctx, "sess-1", strings.NewReader(jsonl), out)
	finalizeTerminal(ctx, "sess-1", res, out, nil)
	close(out)
	var evs []bridge.StreamEvent
	for ev := range out {
		evs = append(evs, ev)
	}
	return evs
}

func kinds(evs []bridge.StreamEvent) []bridge.EventKind {
	ks := make([]bridge.EventKind, len(evs))
	for i, ev := range evs {
		ks[i] = ev.Kind
	}
	return ks
}

func last(evs []bridge.StreamEvent) bridge.StreamEvent { return evs[len(evs)-1] }

func responseText(evs []bridge.StreamEvent) string {
	var b strings.Builder
	for _, ev := range evs {
		if ev.Kind == bridge.EventResponseDelta {
			b.WriteString(ev.Delta)
		}
	}
	return b.String()
}

// readFixture loads a golden transcript from testdata/.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// --- golden fixture tests (offline, no CLI) ---

// TestFixturePong: a plain turn yields the visible answer as a response delta,
// a session status carrying the thread_id, and a done terminal.
func TestFixturePong(t *testing.T) {
	evs := replay(t, readFixture(t, "pong.jsonl"))

	if got := responseText(evs); got != "PONG" {
		t.Fatalf("response = %q, want PONG; kinds=%v", got, kinds(evs))
	}
	if last(evs).Kind != bridge.EventDone {
		t.Fatalf("terminal = %q, want done; kinds=%v", last(evs).Kind, kinds(evs))
	}
	var sawSession bool
	for _, ev := range evs {
		if ev.Kind == bridge.EventStatus && statusName(ev.Status) == StatusSession {
			sawSession = true
		}
	}
	if !sawSession {
		t.Fatalf("no session status event; kinds=%v", kinds(evs))
	}
}

// TestFixtureCommandExecution: the command_execution lifecycle maps to a tool
// call (in_progress) then a tool_done status carrying the exit code, then the
// agent summary, then done.
func TestFixtureCommandExecution(t *testing.T) {
	evs := replay(t, readFixture(t, "command_execution.jsonl"))

	var tc *bridge.StreamEvent
	var toolDone *bridge.StreamEvent
	for i := range evs {
		switch {
		case evs[i].Kind == bridge.EventToolCall:
			tc = &evs[i]
		case evs[i].Kind == bridge.EventStatus && statusName(evs[i].Status) == StatusToolDone:
			toolDone = &evs[i]
		}
	}
	if tc == nil || tc.ToolCall == nil {
		t.Fatalf("no tool-call event; kinds=%v", kinds(evs))
	}
	if tc.ToolCall.Name != "command_execution" || tc.ToolCall.Source != "codex" {
		t.Fatalf("tool call = %+v", tc.ToolCall)
	}
	if !strings.Contains(string(tc.ToolCall.Arguments), "echo hello-codex") {
		t.Fatalf("tool call args = %s", tc.ToolCall.Arguments)
	}
	if toolDone == nil {
		t.Fatalf("no tool_done status; kinds=%v", kinds(evs))
	}
	if !strings.Contains(toolDone.Status, "exit=0") {
		t.Fatalf("tool_done status = %q, want exit=0", toolDone.Status)
	}
	if !strings.Contains(responseText(evs), "hello-codex") {
		t.Fatalf("missing agent message; response=%q", responseText(evs))
	}
	if last(evs).Kind != bridge.EventDone {
		t.Fatalf("terminal = %q, want done", last(evs).Kind)
	}
}

// TestFixtureResumeBanana42: a resume transcript echoes the same thread_id and
// recalls the prior-turn codeword (continuity proof, CONTRACT §2.1).
func TestFixtureResumeBanana42(t *testing.T) {
	evs := replay(t, readFixture(t, "resume_banana42.jsonl"))

	if responseText(evs) != "BANANA42" {
		t.Fatalf("recalled codeword = %q, want BANANA42", responseText(evs))
	}
	if last(evs).Kind != bridge.EventDone {
		t.Fatalf("terminal = %q, want done", last(evs).Kind)
	}
}

// TestFixtureMinimalToolsFailure: the verified failure transcript ends in an
// actionable EventError (NOT done), and the message is unwrapped — not the raw
// escaped JSON blob.
func TestFixtureMinimalToolsFailure(t *testing.T) {
	evs := replay(t, readFixture(t, "minimal_tools_failure.jsonl"))

	if last(evs).Kind != bridge.EventError {
		t.Fatalf("terminal = %q, want error; kinds=%v", last(evs).Kind, kinds(evs))
	}
	for _, ev := range evs {
		if ev.Kind == bridge.EventDone {
			t.Fatalf("a failed turn must never produce done; kinds=%v", kinds(evs))
		}
	}
	msg := last(evs).Error
	if !strings.Contains(msg, "minimal") || !strings.Contains(msg, "tools") {
		t.Fatalf("error not actionable: %q", msg)
	}
}

// --- tolerant-parser tests (prove the contract, not just happy path) ---

// TestTolerantUnknownItemType: an unknown item.type is skipped and surrounding
// known events still map (forward-compat, CONTRACT §3.3).
func TestTolerantUnknownItemType(t *testing.T) {
	const jsonl = `{"type":"thread.started","thread_id":"t"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"i0","type":"web_search","query":"x"}}
{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"after-unknown"}}
{"type":"turn.completed","usage":{}}
`
	evs := replay(t, jsonl)
	if responseText(evs) != "after-unknown" {
		t.Fatalf("known event after unknown item.type lost; response=%q", responseText(evs))
	}
	if last(evs).Kind != bridge.EventDone {
		t.Fatalf("terminal = %q, want done", last(evs).Kind)
	}
}

// TestTolerantMalformedLine: malformed JSON lines (and a half-line) are skipped
// without crashing; the surrounding stream still parses (CONTRACT §5/§8).
func TestTolerantMalformedLine(t *testing.T) {
	const jsonl = `{"type":"thread.started","thread_id":"t"}
this is not json at all
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"survived"}}
{"broken json
{"some":"object","without":"a type field"}
{"type":"turn.completed","usage":{}}
`
	evs := replay(t, jsonl)
	if responseText(evs) != "survived" {
		t.Fatalf("malformed lines broke the stream; response=%q", responseText(evs))
	}
	if last(evs).Kind != bridge.EventDone {
		t.Fatalf("terminal = %q, want done", last(evs).Kind)
	}
}

// TestTolerantUnknownTopLevelEvent: an unknown top-level event type is skipped
// without affecting the rest of the stream.
func TestTolerantUnknownTopLevelEvent(t *testing.T) {
	const jsonl = `{"type":"thread.started","thread_id":"t"}
{"type":"some.future.event","payload":{"x":1}}
{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"ok"}}
{"type":"turn.completed","usage":{}}
`
	evs := replay(t, jsonl)
	if responseText(evs) != "ok" || last(evs).Kind != bridge.EventDone {
		t.Fatalf("unknown top-level event mishandled; response=%q terminal=%q", responseText(evs), last(evs).Kind)
	}
}

// TestReasoningItemMapsToThinking: a reasoning item (not observed on 0.141.0 but
// allowed by the contract) maps to a thinking delta when present.
func TestReasoningItemMapsToThinking(t *testing.T) {
	const jsonl = `{"type":"thread.started","thread_id":"t"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"r0","type":"reasoning","text":"let me think"}}
{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"answer"}}
{"type":"turn.completed","usage":{}}
`
	evs := replay(t, jsonl)
	var sawThinking bool
	for _, ev := range evs {
		if ev.Kind == bridge.EventThinkingDelta && ev.Delta == "let me think" {
			sawThinking = true
		}
	}
	if !sawThinking {
		t.Fatalf("reasoning item did not map to a thinking delta; kinds=%v", kinds(evs))
	}
}

// --- event-authoritative tests (failure decided by stream, not exit code) ---

// TestFailedTurnExitZero: a turn.failed must terminate as an error even though
// the parser never sees an exit code (and the real process could exit 0). This
// is the core event-authoritative invariant (CONTRACT §4).
func TestFailedTurnExitZero(t *testing.T) {
	const jsonl = `{"type":"thread.started","thread_id":"t"}
{"type":"turn.started"}
{"type":"turn.failed","error":{"message":"upstream hiccup"}}
`
	evs := replay(t, jsonl)
	if last(evs).Kind != bridge.EventError {
		t.Fatalf("terminal = %q, want error (event-authoritative)", last(evs).Kind)
	}
	for _, ev := range evs {
		if ev.Kind == bridge.EventDone {
			t.Fatal("turn.failed must never produce a done terminal")
		}
	}
	if !strings.Contains(last(evs).Error, "upstream hiccup") {
		t.Fatalf("terminal error lost the message: %q", last(evs).Error)
	}
}

// TestNoTerminalEvent: a stream that ends with neither turn.completed nor
// turn.failed (e.g. the process was killed) finalizes as an abnormal error,
// never as a silent done.
func TestNoTerminalEvent(t *testing.T) {
	const jsonl = `{"type":"thread.started","thread_id":"t"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"partial"}}
`
	evs := replay(t, jsonl)
	if last(evs).Kind != bridge.EventError {
		t.Fatalf("terminal = %q, want error (abnormal end)", last(evs).Kind)
	}
	if !strings.Contains(last(evs).Error, "abnormal") {
		t.Fatalf("abnormal terminal message = %q", last(evs).Error)
	}
}

// TestItemErrorMarksTurnFailed: an item.type=="error" (e.g. deprecated config)
// flags the turn failed so the terminal stays an error even if a turn.completed
// never arrives.
func TestItemErrorMarksTurnFailed(t *testing.T) {
	const jsonl = `{"type":"thread.started","thread_id":"t"}
{"type":"item.completed","item":{"id":"i0","type":"error","message":"something broke"}}
{"type":"turn.started"}
`
	evs := replay(t, jsonl)
	if last(evs).Kind != bridge.EventError {
		t.Fatalf("terminal = %q, want error", last(evs).Kind)
	}
}

// --- cancellation (mirror cursor/timeout_test.go) ---

// TestCancellationMidStream cancels the context mid-replay and asserts the
// stream stops, the channel closes, and no scanning goroutine leaks.
func TestCancellationMidStream(t *testing.T) {
	before := runtime.NumGoroutine()

	var sb strings.Builder
	sb.WriteString(`{"type":"thread.started","thread_id":"t"}` + "\n")
	sb.WriteString(`{"type":"turn.started"}` + "\n")
	for i := 0; i < 5000; i++ {
		sb.WriteString(`{"type":"item.completed","item":{"id":"x","type":"agent_message","text":"chunk"}}` + "\n")
	}
	sb.WriteString(`{"type":"turn.completed","usage":{}}` + "\n")

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan bridge.StreamEvent, 1) // tiny buffer so the scanner blocks fast

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(out)
		res, _ := scanStream(ctx, "s", strings.NewReader(sb.String()), out)
		finalizeTerminal(ctx, "s", res, out, nil)
	}()

	// Read one event, then cancel and stop consuming the buffer for a moment.
	<-out
	cancel()

	closed := make(chan struct{})
	go func() {
		for range out {
		}
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after ctx cancel (goroutine leak)")
	}
	wg.Wait()

	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Errorf("goroutine count grew from %d to %d (possible leak)", before, after)
	}
}

// TestCancelledTerminalMessage: when ctx is already cancelled, the terminal
// error names the cancellation rather than a generic stream message.
func TestCancelledTerminalMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// turnFailed path with a cancelled ctx should report cancellation.
	got := terminalErrorMessage(ctx, "ignored")
	if !strings.Contains(got, "cancelled") {
		t.Fatalf("cancelled terminal message = %q, want it to mention cancellation", got)
	}
}
