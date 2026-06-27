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
	"github.com/jahrulnr/sapaloq/internal/config"
)

// defaultProviderEntry returns a minimal provider entry that drives the
// provider bridge with the openai parser. Tests can mutate fields as needed.
func defaultProviderEntry() config.LLMBridge {
	return config.LLMBridge{
		Driver:         "provider-bridge",
		Endpoint:       "https://api.example.com",
		Model:          "gpt-4o-mini",
		CredentialsEnv: "OPENAI_API_KEY",
		Parser:         "openai",
	}
}

// TestStreamOpenAIEndToEnd spins up a fake OpenAI server, dispatches the
// streamOpenAI flow, and asserts the bridge receives the right
// thinking/text/tool events in order.
func TestStreamOpenAIEndToEnd(t *testing.T) {
	var capturedAuth string
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		write := func(payload string) {
			_, _ = w.Write([]byte("data: " + payload + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}

		// Reasoning + text + tool call in one stream.
		write(`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"plan: "}}]}`)
		write(`{"choices":[{"index":0,"delta":{"reasoning_content":"think harder"}}]}`)
		write(`{"choices":[{"index":0,"delta":{"content":"hello "}}]}`)
		write(`{"choices":[{"index":0,"delta":{"content":"world"}}]}`)
		write(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"echo","arguments":"{\"x\":"}}]}}]}`)
		write(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`)
		write(`[DONE]`)
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
		Stream:        true,
	}

	var events []WireEvent
	err := streamOpenAI(context.Background(), opts, func(ev WireEvent) bool {
		events = append(events, ev)
		return true
	})
	if err != nil {
		t.Fatalf("streamOpenAI: %v", err)
	}

	if !strings.HasPrefix(capturedAuth, "Bearer ") {
		t.Fatalf("auth header wrong: %q", capturedAuth)
	}

	// Validate body shape: should carry stream=true, our model, and a tools array.
	var req openAIRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if !req.Stream {
		t.Error("stream flag must be set")
	}
	if req.Model != "gpt-4o-mini" {
		t.Errorf("model: %s", req.Model)
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "echo" {
		t.Errorf("tools missing echo: %+v", req.Tools)
	}

	// Validate emitted events: two thinking, two text, one tool.
	var thinking, text, tools int
	var toolName string
	for _, ev := range events {
		switch {
		case ev.Thinking != "":
			thinking++
		case ev.Text != "":
			text++
		case ev.Tool.Name != "":
			tools++
			toolName = ev.Tool.Name
		}
	}
	if thinking != 2 {
		t.Errorf("thinking events: want 2, got %d", thinking)
	}
	if text != 2 {
		t.Errorf("text events: want 2, got %d", text)
	}
	if tools != 1 {
		t.Errorf("tool events: want 1, got %d", tools)
	}
	if toolName != "echo" {
		t.Errorf("tool name: %s", toolName)
	}
}

// TestStreamKimiEndToEnd verifies the Kimi wire is exercised with a thinking
// flag injected and the same delta shape as OpenAI.
func TestStreamKimiEndToEnd(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(payload string) {
			_, _ = w.Write([]byte("data: " + payload + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		write(`{"choices":[{"index":0,"delta":{"reasoning_content":"r1"}}]}`)
		write(`{"choices":[{"index":0,"delta":{"content":"hi"}}]}`)
		write(`[DONE]`)
	}))
	defer server.Close()

	opts := WireOptions{
		Parser:          ParserKimi,
		Auth:            AuthBearer,
		Endpoint:        server.URL,
		Token:           "sk-test",
		Model:           "kimi-k2.6",
		Messages:        []bridge.Message{{Role: "user", Content: "hi"}},
		ReasoningEffort: "high",
		Timeout:         5 * time.Second,
		Stream:          true,
	}

	var events []WireEvent
	_ = streamKimi(context.Background(), opts, func(ev WireEvent) bool {
		events = append(events, ev)
		return true
	})

	var rawBody map[string]any
	if err := json.Unmarshal(capturedBody, &rawBody); err != nil {
		t.Fatalf("decode kimi body: %v", err)
	}
	thinking, ok := rawBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking field missing in kimi body: %+v", rawBody["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Errorf("thinking.type: %v", thinking["type"])
	}
	if got := len(events); got != 2 {
		t.Errorf("events: want 2, got %d", got)
	}
}

// TestStreamClaudeEndToEnd validates the Anthropic wire dispatch.
func TestUpstreamErrorBodyHidesHTML(t *testing.T) {
	got := upstreamErrorBody([]byte("<!DOCTYPE html>\n<html><head><title>Login</title></head><body>nope</body></html>"))
	if strings.Contains(got, "<html") || strings.Contains(got, "DOCTYPE") {
		t.Fatalf("raw HTML leaked: %q", got)
	}
	if !strings.Contains(got, "HTML error page") {
		t.Fatalf("unexpected sanitized body: %q", got)
	}
}

func TestStreamClaudeEndToEnd(t *testing.T) {
	var capturedAuth string
	var capturedVersion string
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("x-api-key")
		capturedVersion = r.Header.Get("anthropic-version")
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(payload string) {
			_, _ = w.Write([]byte("data: " + payload + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		// A tool_use block lifecycle.
		write(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"echo"}}`)
		write(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`)
		write(`{"type":"content_block_stop","index":0}`)
		// A text block.
		write(`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)
		write(`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hello"}}`)
		write(`{"type":"content_block_stop","index":1}`)
		_ = flusher
	}))
	defer server.Close()

	opts := WireOptions{
		Parser:     ParserClaude,
		Auth:       AuthXAPIKey,
		APIVersion: "2023-06-01",
		Endpoint:   server.URL,
		Token:      "sk-ant-test",
		Model:      "claude-sonnet-4-5",
		Messages: []bridge.Message{
			{Role: "system", Content: "system one"},
			{Role: "checkpoint", Content: "system two"},
			{Role: "user", Content: "hi"},
		},
		Timeout: 5 * time.Second,
		Stream:  true,
	}

	var events []WireEvent
	err := streamClaude(context.Background(), opts, func(ev WireEvent) bool {
		events = append(events, ev)
		return true
	})
	if err != nil {
		t.Fatalf("streamClaude: %v", err)
	}
	if capturedAuth != "sk-ant-test" {
		t.Errorf("x-api-key header: %q", capturedAuth)
	}
	if capturedVersion != "2023-06-01" {
		t.Errorf("anthropic-version: %q", capturedVersion)
	}

	// Validate body shape: must carry max_tokens, model, messages.
	var req claudeRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.MaxTokens == 0 {
		t.Error("max_tokens must be set on Claude requests")
	}
	if req.Model != "claude-sonnet-4-5" {
		t.Errorf("model: %s", req.Model)
	}
	if req.System != "system one\n\nsystem two" {
		t.Errorf("top-level system = %q", req.System)
	}
	for _, message := range req.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			t.Errorf("invalid Anthropic message role %q", message.Role)
		}
	}

	// Expect one tool + one text event.
	var toolCount, textCount int
	for _, ev := range events {
		if ev.Tool.Name != "" {
			toolCount++
		}
		if ev.Text != "" {
			textCount++
		}
	}
	if toolCount != 1 {
		t.Errorf("tool events: want 1, got %d", toolCount)
	}
	if textCount != 1 {
		t.Errorf("text events: want 1, got %d", textCount)
	}
}

// TestRunSSEError verifies the wire layer surfaces upstream status codes.
func TestRunSSEError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadRequest)
	}))
	defer server.Close()
	opts := WireOptions{
		Parser:   ParserOpenAI,
		Auth:     AuthBearer,
		Endpoint: server.URL,
		Token:    "x",
		Timeout:  2 * time.Second,
	}
	err := runSSE(context.Background(), opts, []byte("{}"), func(line []byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "upstream status 400") {
		t.Fatalf("expected status 400 error, got %v", err)
	}
}

// TestBridgeCapsReflectsToken guards the LiveAPI gating.
func TestBridgeCapsReflectsToken(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	// Build a minimal entry; Bridge.New is checked separately.
	b := &Bridge{entry: defaultProviderEntry()}
	caps := b.Caps()
	if !caps.LiveAPI {
		t.Error("LiveAPI must be true when token env is set")
	}
	if !caps.Thinking {
		t.Error("Thinking must be true for openai parser")
	}

	t.Setenv("OPENAI_API_KEY", "")
	if got := (&Bridge{entry: defaultProviderEntry()}).Caps().LiveAPI; got {
		t.Error("LiveAPI must be false when token env is empty")
	}
}

// TestExtractDataPayload covers the SSE prefix helper.
func TestExtractDataPayload(t *testing.T) {
	cases := []struct {
		in       string
		wantOk   bool
		wantBody string
	}{
		{"data: hello", true, "hello"},
		{"data:  spaced", true, "spaced"},
		{"data:[DONE]", true, "[DONE]"},
		{"event: ping", false, ""},
		{"", false, ""},
		{": comment", false, ""},
	}
	for _, tc := range cases {
		got, ok := extractDataPayload([]byte(tc.in))
		if ok != tc.wantOk {
			t.Errorf("extractDataPayload(%q) ok=%v want %v", tc.in, ok, tc.wantOk)
			continue
		}
		if !ok {
			continue
		}
		if string(got) != tc.wantBody {
			t.Errorf("extractDataPayload(%q) body=%q want %q", tc.in, got, tc.wantBody)
		}
	}
}

// suppress unused import warnings on bridge / config when other test
// helpers are pruned.
var (
	_ = bridge.Message{}
	_ = config.LLMBridge{}
)
