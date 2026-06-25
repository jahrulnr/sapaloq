package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// collectComplete runs Stream() in non-stream mode against the given server and
// returns the emitted WireEvents plus the captured request body.
func collectComplete(t *testing.T, opts WireOptions) ([]WireEvent, []byte) {
	t.Helper()
	var events []WireEvent
	err := Stream(context.Background(), opts, func(ev WireEvent) bool {
		events = append(events, ev)
		return true
	})
	if err != nil {
		t.Fatalf("Stream(non-stream): %v", err)
	}
	return events, nil
}

// TestCompleteOpenAINonStream verifies the OpenAI/Kimi-shaped non-stream
// response is parsed into thinking, text, and tool-call events, and that the
// request body carries stream:false.
func TestCompleteOpenAINonStream(t *testing.T) {
	var capturedBody []byte
	var capturedAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		capturedAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"reasoning_content": "let me think",
					"content": "hello world",
					"tool_calls": [{
						"index": 0,
						"id": "call_1",
						"type": "function",
						"function": {"name": "echo", "arguments": "{\"x\":1}"}
					}]
				},
				"finish_reason": "tool_calls"
			}]
		}`))
	}))
	defer server.Close()

	opts := WireOptions{
		Parser:        ParserOpenAI,
		Auth:          AuthBearer,
		Endpoint:      server.URL,
		Token:         "sk-test",
		Model:         "gpt-4o-mini",
		Messages:      []bridge.Message{{Role: "user", Content: "hi"}},
		DeclaredTools: []string{"echo"},
		Timeout:       5 * time.Second,
		Stream:        false,
	}
	events, _ := collectComplete(t, opts)

	// Body must declare stream:false.
	var req openAIRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if req.Stream {
		t.Error("non-stream body must carry stream:false")
	}
	if capturedAccept != "application/json" {
		t.Errorf("Accept header: want application/json, got %q", capturedAccept)
	}

	assertOneEachThinkingTextTool(t, events, "let me think", "hello world", "echo")
}

// TestCompleteKimiNonStream verifies the Kimi parser shares the Chat
// Completions non-stream shape (reasoning_content + content).
func TestCompleteKimiNonStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {"reasoning_content": "r1", "content": "kimi says hi"}
			}]
		}`))
	}))
	defer server.Close()

	opts := WireOptions{
		Parser:   ParserKimi,
		Auth:     AuthBearer,
		Endpoint: server.URL,
		Token:    "sk-test",
		Model:    "kimi-k2.6",
		Messages: []bridge.Message{{Role: "user", Content: "hi"}},
		Timeout:  5 * time.Second,
		Stream:   false,
	}
	events, _ := collectComplete(t, opts)

	var thinking, text string
	for _, ev := range events {
		if ev.Thinking != "" {
			thinking = ev.Thinking
		}
		if ev.Text != "" {
			text = ev.Text
		}
	}
	if thinking != "r1" {
		t.Errorf("thinking: want r1, got %q", thinking)
	}
	if text != "kimi says hi" {
		t.Errorf("text: want 'kimi says hi', got %q", text)
	}
}

// TestCompleteClaudeNonStream verifies the Anthropic content[] block list is
// parsed into thinking, text, and tool_use events in order.
func TestCompleteClaudeNonStream(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"content": [
				{"type": "thinking", "thinking": "reasoning here"},
				{"type": "text", "text": "the answer"},
				{"type": "tool_use", "id": "tu_1", "name": "lookup", "input": {"q": "go"}}
			],
			"stop_reason": "tool_use"
		}`))
	}))
	defer server.Close()

	opts := WireOptions{
		Parser:        ParserClaude,
		Auth:          AuthXAPIKey,
		APIVersion:    "2023-06-01",
		Endpoint:      server.URL,
		Token:         "sk-ant-test",
		Model:         "claude-sonnet-4-5",
		Messages:      []bridge.Message{{Role: "user", Content: "hi"}},
		DeclaredTools: []string{"lookup"},
		Timeout:       5 * time.Second,
		Stream:        false,
	}
	events, _ := collectComplete(t, opts)

	var req claudeRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("decode claude body: %v", err)
	}
	if req.Stream {
		t.Error("non-stream claude body must carry stream:false")
	}

	assertOneEachThinkingTextTool(t, events, "reasoning here", "the answer", "lookup")
}

// TestCompleteEmptyContent verifies a response with no content (a bare done)
// yields no events and no error.
func TestCompleteEmptyContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""}}]}`))
	}))
	defer server.Close()

	opts := WireOptions{
		Parser:   ParserOpenAI,
		Auth:     AuthBearer,
		Endpoint: server.URL,
		Token:    "sk-test",
		Messages: []bridge.Message{{Role: "user", Content: "hi"}},
		Timeout:  5 * time.Second,
		Stream:   false,
	}
	events, _ := collectComplete(t, opts)
	if len(events) != 0 {
		t.Errorf("empty response should yield no events, got %d", len(events))
	}
}

// TestCompleteRetriesTransient500 proves the non-stream path reuses the
// pre-stream retry budget: two 500s then a healthy 200 succeed.
func TestCompleteRetriesTransient500(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"transient"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"recovered"}}]}`))
	}))
	defer server.Close()

	opts := WireOptions{
		Parser:     ParserOpenAI,
		Auth:       AuthBearer,
		Endpoint:   server.URL,
		Token:      "sk-test",
		Messages:   []bridge.Message{{Role: "user", Content: "hi"}},
		Timeout:    5 * time.Second,
		MaxRetries: 5,
		Stream:     false,
	}
	events, _ := collectComplete(t, opts)
	if hits != 3 {
		t.Fatalf("expected 3 upstream hits (2 fail + 1 ok), got %d", hits)
	}
	if len(events) != 1 || events[0].Text != "recovered" {
		t.Fatalf("expected one 'recovered' text event, got %+v", events)
	}
}

// TestCompleteNonRetryable400 proves a definitive 4xx is surfaced without
// retrying.
func TestCompleteNonRetryable400(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	opts := WireOptions{
		Parser:     ParserOpenAI,
		Auth:       AuthBearer,
		Endpoint:   server.URL,
		Token:      "sk-test",
		Messages:   []bridge.Message{{Role: "user", Content: "hi"}},
		Timeout:    5 * time.Second,
		MaxRetries: 5,
		Stream:     false,
	}
	err := Stream(context.Background(), opts, func(ev WireEvent) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "upstream status 400") {
		t.Fatalf("expected status 400 error, got %v", err)
	}
	if hits != 1 {
		t.Fatalf("a 400 must not be retried: expected 1 hit, got %d", hits)
	}
}

// assertOneEachThinkingTextTool checks the event slice has exactly one thinking,
// one text, and one tool event with the expected payloads.
func assertOneEachThinkingTextTool(t *testing.T, events []WireEvent, wantThinking, wantText, wantTool string) {
	t.Helper()
	var thinking, text, tools int
	var gotThinking, gotText, gotTool string
	for _, ev := range events {
		switch {
		case ev.Thinking != "":
			thinking++
			gotThinking = ev.Thinking
		case ev.Text != "":
			text++
			gotText = ev.Text
		case ev.Tool.Name != "":
			tools++
			gotTool = ev.Tool.Name
		}
	}
	if thinking != 1 || gotThinking != wantThinking {
		t.Errorf("thinking: want 1x %q, got %dx %q", wantThinking, thinking, gotThinking)
	}
	if text != 1 || gotText != wantText {
		t.Errorf("text: want 1x %q, got %dx %q", wantText, text, gotText)
	}
	if tools != 1 || gotTool != wantTool {
		t.Errorf("tool: want 1x %q, got %dx %q", wantTool, tools, gotTool)
	}
}
