package provider

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

// TestStreamTTFBTimeoutFires proves the time-to-first-byte guard: a server that
// accepts the TCP connection but NEVER sends response headers (a common
// overloaded-gateway failure) must be abandoned within the idle window. Before
// the guard, the SSE idle timer hadn't started yet (it only arms after headers
// arrive), so this hung up to the 600s whole-request timeout with no heartbeat —
// long enough for the worker watchdog to force-fail a healthy sub-agent.
func TestStreamTTFBTimeoutFires(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	// Accept connections and hold them open without ever replying.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Keep the conn referenced but silent; closed when listener closes.
			_ = c
		}
	}()

	opts := WireOptions{
		Parser:      ParserOpenAI,
		Auth:        AuthBearer,
		Endpoint:    "http://" + ln.Addr().String() + "/v1/chat/completions",
		Token:       "sk-test",
		Model:       "gpt-4o-mini",
		Messages:    []bridge.Message{{Role: "user", Content: "hi"}},
		Timeout:     30 * time.Second,
		IdleTimeout: 400 * time.Millisecond,
	}

	start := time.Now()
	err = streamOpenAI(context.Background(), opts, func(ev WireEvent) bool { return true })
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("a server that never sends headers must error, got nil")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("TTFB timeout took too long (idle guard not applied): %v", elapsed)
	}
}

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

// TestStreamIdleTimeoutFiresOnKeepAliveOnly proves the keep-alive fix: a server
// that, after one real chunk, sends ONLY blank-line keep-alives (no model data)
// must still trip the idle timeout. Before the fix every blank line reset the
// idle timer, so a stream that delivered nothing but newlines stayed "alive"
// forever — the exact "listening but receiving nothing from the model" stall.
func TestStreamIdleTimeoutFiresOnKeepAliveOnly(t *testing.T) {
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		// Now spam blank-line keep-alives only — no model data ever again.
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_, err := w.Write([]byte("\n"))
				if err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}))
	defer server.Close()
	defer close(stop)

	opts := WireOptions{
		Parser:      ParserOpenAI,
		Auth:        AuthBearer,
		Endpoint:    server.URL,
		Token:       "sk-test",
		Model:       "gpt-4o-mini",
		Messages:    []bridge.Message{{Role: "user", Content: "hi"}},
		Timeout:     30 * time.Second,
		IdleTimeout: 400 * time.Millisecond,
	}

	start := time.Now()
	err := streamOpenAI(context.Background(), opts, func(ev WireEvent) bool { return true })
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "SSE idle timeout") {
		t.Fatalf("keep-alive-only stream must trip idle timeout, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("idle timeout took too long despite keep-alives: %v", elapsed)
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
