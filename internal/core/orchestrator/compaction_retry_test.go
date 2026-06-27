package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestLooksLikeContextOverflow(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"provider-bridge: upstream status 400: maximum context length exceeded", true},
		{"provider-bridge: upstream status 400: this model's context window is 8192 tokens", true},
		{"provider-bridge: upstream status 400: too many tokens in request", true},
		{"provider-bridge: upstream status 400: reduce the length of the messages", true},
		{"provider-bridge: upstream status 400: rate limit exceeded", false},
		{"provider-bridge: upstream status 400: this model does not support image input", false},
		{"provider-bridge: upstream status 401: invalid api key", false},
		{"connection reset", false},
	}
	for _, tc := range cases {
		if got := looksLikeContextOverflow(tc.msg); got != tc.want {
			t.Errorf("looksLikeContextOverflow(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
	// Cross-contamination guard: a context-overflow 400 must NOT be treated as
	// an image rejection, and a vision reject must NOT look like an overflow.
	if imageRejection("provider-bridge: upstream status 400: maximum context length exceeded") {
		t.Errorf("context-overflow 400 must not be classed as image rejection")
	}
	if looksLikeContextOverflow("provider-bridge: upstream status 400: this model does not support image input") {
		t.Errorf("vision reject must not be classed as context overflow")
	}
}

// overflowBridge returns a context-overflow 400 on its first N calls, then a
// plain text answer. It records each request so a test can assert the retried
// request shrank.
type overflowBridge struct {
	failFirst int
	requests  []bridge.Request
}

func (b *overflowBridge) ID() string              { return "overflow" }
func (b *overflowBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *overflowBridge) Complete(_ context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.requests = append(b.requests, req)
	call := len(b.requests)
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		if call <= b.failFirst {
			out <- bridge.StreamEvent{Kind: bridge.EventError, Error: "provider-bridge: upstream status 400: maximum context length exceeded"}
		} else {
			out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "compacted answer"}
			stop := parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`)}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &stop}
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

func longHistory(n int) []bridge.Message {
	msgs := make([]bridge.Message, 0, n)
	msgs = append(msgs, bridge.Message{Role: "system", Content: "you are a helpful assistant"})
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, bridge.Message{Role: role, Content: fmt.Sprintf("turn %d: %s", i, strings.Repeat("blah ", 40))})
	}
	return msgs
}

func drain(out chan bridge.StreamEvent) {
	go func() {
		for range out {
		}
	}()
}

func TestRunConversationCompactsAndRetriesOnContextOverflow(t *testing.T) {
	fake := &overflowBridge{failFirst: 1}
	orch := &Orchestrator{memoryDir: t.TempDir(), vision: make(map[string]bool), active: make(map[string]*activeRun)}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "test", Model: "model"}, br: fake}
	out := make(chan bridge.StreamEvent, 64)
	drain(out)

	result, err := orch.runConversation(context.Background(), snap, out, "session", "the original task", longHistory(20), nil)
	close(out)
	if err != nil {
		t.Fatalf("expected graceful compaction+retry, got error: %v", err)
	}
	if result.String() != "compacted answer" {
		t.Fatalf("answer = %q, want compacted answer", result.String())
	}
	if len(fake.requests) != 2 {
		t.Fatalf("expected 2 calls (overflow then retry), got %d", len(fake.requests))
	}
	if len(fake.requests[1].Messages) >= len(fake.requests[0].Messages) {
		t.Fatalf("retry should send fewer messages: first=%d retry=%d",
			len(fake.requests[0].Messages), len(fake.requests[1].Messages))
	}
}

func TestRunConversationOverflowSurfacesWhenAlreadyMinimal(t *testing.T) {
	// A tiny conversation can't be compacted further, so the overflow error
	// must surface instead of looping.
	fake := &overflowBridge{failFirst: 99}
	orch := &Orchestrator{memoryDir: t.TempDir(), vision: make(map[string]bool), active: make(map[string]*activeRun)}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "test", Model: "model"}, br: fake}
	out := make(chan bridge.StreamEvent, 64)
	drain(out)

	_, err := orch.runConversation(context.Background(), snap, out, "session", "task",
		[]bridge.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}}, nil)
	close(out)
	if err == nil {
		t.Fatalf("expected an error when conversation can't be compacted")
	}
	if !strings.Contains(err.Error(), "already minimal") {
		t.Fatalf("expected 'already minimal' error, got: %v", err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("must not retry an un-compactable conversation, got %d calls", len(fake.requests))
	}
}

func TestRunConversationOverflowBoundedRetries(t *testing.T) {
	// Always-overflow against a large history: the run must stop after the
	// bounded number of forced compactions and never loop forever. The error
	// surfaces as a stream EventError (per the package convention that the
	// turn function returns nil once it has emitted the error event).
	fake := &overflowBridge{failFirst: 99}
	orch := &Orchestrator{memoryDir: t.TempDir(), vision: make(map[string]bool), active: make(map[string]*activeRun)}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "test", Model: "model"}, br: fake}

	out := make(chan bridge.StreamEvent, 256)
	var sawError bool
	done := make(chan struct{})
	go func() {
		for ev := range out {
			if ev.Kind == bridge.EventError {
				sawError = true
			}
			if ev.Kind == bridge.EventTranscript && ev.Transcript != nil {
				for _, e := range ev.Transcript.Entries {
					if e.Kind == bridge.TranscriptError {
						sawError = true
					}
				}
			}
		}
		close(done)
	}()

	_, _ = orch.runConversation(context.Background(), snap, out, "session", "task", longHistory(60), nil)
	close(out)
	<-done

	// 1 initial + up to maxForcedCompactions (3) retries = at most 4 calls,
	// and it may stop earlier if compaction hits the minimal floor.
	if len(fake.requests) == 0 || len(fake.requests) > 4 {
		t.Fatalf("forced compactions not bounded: %d upstream calls (want 1..4)", len(fake.requests))
	}
	if !sawError {
		t.Fatalf("expected the overflow error to surface as a stream EventError")
	}
}
