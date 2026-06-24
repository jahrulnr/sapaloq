package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// writeSSEOK writes a minimal valid OpenAI SSE stream (one text delta + DONE).
func writeSSEOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	write := func(payload string) {
		_, _ = w.Write([]byte("data: " + payload + "\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
	write(`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`)
	write(`[DONE]`)
}

func retryOpts(endpoint string, maxRetries int) WireOptions {
	return WireOptions{
		Parser:     ParserOpenAI,
		Auth:       AuthBearer,
		Endpoint:   endpoint,
		Token:      "sk-test",
		Model:      "gpt-4o-mini",
		Messages:   []bridge.Message{{Role: "user", Content: "hi"}},
		Timeout:    5 * time.Second,
		MaxRetries: maxRetries,
	}
}

// TestRunSSERetriesTransient500ThenSucceeds proves a flaky gateway (two 500s
// then a healthy 200) is absorbed by the pre-stream retry loop. This mirrors
// the real Vercel-AI-gateway `500 Connection error` seen routing opus-4.8.
func TestRunSSERetriesTransient500ThenSucceeds(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			http.Error(w, `{"error":{"message":"Connection error","code":"500"}}`, http.StatusInternalServerError)
			return
		}
		writeSSEOK(w)
	}))
	defer server.Close()

	var lines int
	err := runSSE(context.Background(), retryOpts(server.URL, 5), []byte("{}"), func(line []byte) error {
		lines++
		return nil
	})
	if err != nil {
		t.Fatalf("runSSE should succeed after retries, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("expected 3 upstream hits (2 fail + 1 ok), got %d", got)
	}
	if lines == 0 {
		t.Fatalf("expected at least one SSE line dispatched on success")
	}
}

// TestRunSSEExhaustsRetriesOn500 verifies a persistently failing gateway gives
// up after exactly MaxRetries+1 attempts and surfaces the status error.
func TestRunSSEExhaustsRetriesOn500(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	const maxRetries = 3
	err := runSSE(context.Background(), retryOpts(server.URL, maxRetries), []byte("{}"), func(line []byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "upstream status 500") {
		t.Fatalf("expected status 500 error, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != maxRetries+1 {
		t.Fatalf("expected %d attempts, got %d", maxRetries+1, got)
	}
}

// TestRunSSEDoesNotRetry4xx confirms a non-retryable client error fails on the
// first attempt (no point retrying a 400/401/403).
func TestRunSSEDoesNotRetry4xx(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer server.Close()

	err := runSSE(context.Background(), retryOpts(server.URL, 5), []byte("{}"), func(line []byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "upstream status 400") {
		t.Fatalf("expected status 400 error, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("4xx must not be retried: expected 1 attempt, got %d", got)
	}
}

// TestRunSSENoRetryAfterStreamStarted guards the streaming-safety invariant:
// once the upstream returns 200 and the stream begins, a mid-stream failure is
// NOT retried (retrying would duplicate already-emitted deltas). Here the
// handler returns 200 then closes the connection abruptly mid-stream.
func TestRunSSENoRetryAfterStreamStarted(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: " + `{"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		// Abruptly hijack and close so the client sees a mid-stream read error.
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	defer server.Close()

	var got []byte
	_ = runSSE(context.Background(), retryOpts(server.URL, 5), []byte("{}"), func(line []byte) error {
		got = append(got, line...)
		return nil
	})
	// Whether the read ends in EOF (nil) or error, the contract is: exactly one
	// attempt - no retry once the stream has started.
	if h := atomic.LoadInt32(&hits); h != 1 {
		t.Fatalf("mid-stream failure must not be retried: expected 1 attempt, got %d", h)
	}
}

// TestRunSSERetryHonoursContextCancel verifies the backoff wait aborts promptly
// when the caller cancels the context.
func TestRunSSERetryHonoursContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel shortly after the first failure, while the loop is in backoff.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := runSSE(ctx, retryOpts(server.URL, 5), []byte("{}"), func(line []byte) error { return nil })
	if err == nil {
		t.Fatalf("expected an error after cancellation")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancellation should abort backoff quickly, took %v", elapsed)
	}
}

// TestRunSSEDisabledRetries confirms MaxRetries=0 means a single attempt.
func TestRunSSEDisabledRetries(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	_ = runSSE(context.Background(), retryOpts(server.URL, 0), []byte("{}"), func(line []byte) error { return nil })
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("MaxRetries=0 must do exactly 1 attempt, got %d", got)
	}
}

// TestIsRetryableStatus documents the retryable classification.
func TestIsRetryableStatus(t *testing.T) {
	retry := []int{408, 429, 500, 502, 503, 504, 599}
	noRetry := []int{200, 201, 301, 400, 401, 403, 404, 422}
	for _, c := range retry {
		if !isRetryableStatus(c) {
			t.Errorf("status %d should be retryable", c)
		}
	}
	for _, c := range noRetry {
		if isRetryableStatus(c) {
			t.Errorf("status %d should NOT be retryable", c)
		}
	}
}
