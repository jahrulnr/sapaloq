package llamacpp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

func TestCompleteWithoutToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected Authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	entry := config.LLMBridge{
		Driver:   driverID,
		Endpoint: server.URL,
		Model:    "test-model",
		Stream:   boolPtr(false),
	}
	b, err := New(entry)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := b.Complete(context.Background(), bridge.Request{
		SessionID: "s1",
		Model:     "test-model",
		Messages:  []bridge.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []bridge.EventKind
	for ev := range ch {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == bridge.EventError {
			t.Fatalf("unexpected error: %s", ev.Error)
		}
	}
	if len(kinds) == 0 {
		t.Fatal("no events")
	}
}

func TestCompleteNostreamFixtureShape(t *testing.T) {
	const fixture = `{
		"choices": [{
			"finish_reason": "tool_calls",
			"message": {
				"role": "assistant",
				"content": "",
				"reasoning_content": "plan step by step",
				"tool_calls": [{
					"id": "call_1",
					"type": "function",
					"function": {"name": "get_weather", "arguments": "{\"city\":\"Jakarta\"}"}
				}]
			}
		}]
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixture))
	}))
	defer server.Close()

	entry := config.LLMBridge{
		Driver:          driverID,
		Endpoint:        server.URL,
		Model:           "unsloth/gemma-4-E2B-it-GGUF:Q4_K_XL",
		ReasoningEffort: "low",
		Stream:          boolPtr(false),
	}
	b, err := New(entry)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := b.Complete(context.Background(), bridge.Request{
		SessionID:     "s1",
		Model:         entry.Model,
		Messages:      []bridge.Message{{Role: "user", Content: "weather Jakarta"}},
		DeclaredTools: []string{"get_weather"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var thinking, toolName string
	for ev := range ch {
		switch ev.Kind {
		case bridge.EventThinkingDelta:
			thinking += ev.Delta
		case bridge.EventToolCall:
			if ev.ToolCall != nil {
				toolName = ev.ToolCall.Name
			}
		case bridge.EventError:
			t.Fatalf("error: %s", ev.Error)
		}
	}
	if !strings.Contains(thinking, "plan") {
		t.Fatalf("expected thinking from reasoning_content, got %q", thinking)
	}
	if toolName != "get_weather" {
		t.Fatalf("tool name %q", toolName)
	}
}

func TestDoctorHealthOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/models/load":
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg, err := Doctor(context.Background(), config.LLMBridge{
		Endpoint: server.URL,
		Model:    "m1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "health ok") {
		t.Fatalf("msg %q", msg)
	}
}

func TestDoctorHealthRefused(t *testing.T) {
	_, err := Doctor(context.Background(), config.LLMBridge{
		Endpoint: "http://127.0.0.1:1",
		Model:    "m1",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDoctorModelsLoadSuccess(t *testing.T) {
	var loadBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/models/load":
			loadBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg, err := Doctor(context.Background(), config.LLMBridge{
		Endpoint: server.URL,
		Model:    "router-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "models/load ok") {
		t.Fatalf("msg %q", msg)
	}
	var payload map[string]any
	if err := json.Unmarshal(loadBody, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "router-model" {
		t.Fatalf("load body %v", payload)
	}
}

func boolPtr(v bool) *bool { return &v }
