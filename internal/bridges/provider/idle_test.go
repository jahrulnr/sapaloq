package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// TestStreamIdleTimeoutFires proves the bug fix: a server that accepts the SSE
// connection, sends one chunk, then goes silent must be abandoned within the
// idle window — NOT held open until the (much larger) whole-request timeout.
// Before the fix this read blocked for up to RequestTimeout (600s default),
// long enough for the worker health watchdog to force-fail the sub-agent.
func TestStreamIdleTimeoutFires(t *testing.T) {
	hang := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"content":"You're right, let me actually"}}]}` + "\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		// Now go silent — the client must give up via the idle timeout.
		<-hang
	}))
	defer server.Close()
	defer close(hang)

	opts := WireOptions{
		Parser:      ParserOpenAI,
		Auth:        AuthBearer,
		Endpoint:    server.URL,
		Token:       "sk-test",
		Model:       "gpt-4o-mini",
		Messages:    []bridge.Message{{Role: "user", Content: "hi"}},
		Timeout:     30 * time.Second, // generous whole-request cap
		IdleTimeout: 300 * time.Millisecond,
	}

	start := time.Now()
	var got []WireEvent
	err := streamOpenAI(context.Background(), opts, func(ev WireEvent) bool {
		got = append(got, ev)
		return true
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected idle-timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "SSE idle timeout") {
		t.Fatalf("expected SSE idle timeout error, got: %v", err)
	}
	// Must give up close to the idle window, well before the 30s request cap.
	if elapsed > 5*time.Second {
		t.Fatalf("idle timeout took too long: %v (should be ~idle window)", elapsed)
	}
	// The first chunk should still have been delivered before the hang.
	if len(got) == 0 || got[0].Text == "" {
		t.Fatalf("expected the pre-hang text chunk to be delivered, got %#v", got)
	}
}

// TestStreamIdleTimeoutDoesNotFireWhenDataFlows confirms a steadily-streaming
// server (chunks within the idle window) completes normally — the idle timer is
// reset on every event and never trips.
func TestStreamIdleTimeoutDoesNotFireWhenDataFlows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(payload string) {
			_, _ = w.Write([]byte("data: " + payload + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		for i := 0; i < 4; i++ {
			write(`{"choices":[{"index":0,"delta":{"content":"tok "}}]}`)
			time.Sleep(120 * time.Millisecond) // < idle window
		}
		write(`[DONE]`)
	}))
	defer server.Close()

	opts := WireOptions{
		Parser:      ParserOpenAI,
		Auth:        AuthBearer,
		Endpoint:    server.URL,
		Token:       "sk-test",
		Model:       "gpt-4o-mini",
		Messages:    []bridge.Message{{Role: "user", Content: "hi"}},
		Timeout:     30 * time.Second,
		IdleTimeout: 500 * time.Millisecond,
	}

	var text strings.Builder
	err := streamOpenAI(context.Background(), opts, func(ev WireEvent) bool {
		text.WriteString(ev.Text)
		return true
	})
	if err != nil {
		t.Fatalf("steady stream should not error: %v", err)
	}
	if strings.TrimSpace(text.String()) == "" {
		t.Fatalf("expected accumulated text, got empty")
	}
}

// TestStreamIdleTimeoutResolverClamps verifies the config invariant: the idle
// timeout never exceeds the whole-request timeout, and defaults are sane.
func TestStreamIdleTimeoutResolverClamps(t *testing.T) {
	// Default idle should be DefaultStreamIdleTimeoutSec.
	if got := (config.LLMBridge{}).StreamIdleTimeout(); got != time.Duration(config.DefaultStreamIdleTimeoutSec)*time.Second {
		t.Fatalf("default idle = %v, want %ds", got, config.DefaultStreamIdleTimeoutSec)
	}
	// Idle larger than request must clamp to request.
	b := config.LLMBridge{RequestTimeoutSec: 10, StreamIdleTimeoutSec: 999}
	if got := b.StreamIdleTimeout(); got != 10*time.Second {
		t.Fatalf("idle should clamp to request (10s), got %v", got)
	}
}
