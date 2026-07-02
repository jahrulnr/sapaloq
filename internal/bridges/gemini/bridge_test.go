package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

func TestMergeResponsePreservesThoughtSignature(t *testing.T) {
	const sample = `{"candidates":[{"content":{"parts":[{"text":"reasoning","thought":true},{"functionCall":{"name":"get_weather","args":{"city":"Jakarta"},"id":"w6smjznv"},"thoughtSignature":"sig-abc"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"thoughtsTokenCount":70}}`
	var resp response
	if err := json.Unmarshal([]byte(sample), &resp); err != nil {
		t.Fatal(err)
	}
	var turn turnAccum
	mergeResponse(&turn, resp)
	if len(turn.modelParts) != 2 {
		t.Fatalf("modelParts = %d, want 2", len(turn.modelParts))
	}
	if turn.modelParts[1].ThoughtSignature != "sig-abc" {
		t.Fatalf("thoughtSignature = %q", turn.modelParts[1].ThoughtSignature)
	}
}

func TestMessagesReplayWireMeta(t *testing.T) {
	meta := encodeWireMeta([]part{{
		FunctionCall:     &functionCall{ID: "id1", Name: "get_weather", Args: json.RawMessage(`{"city":"Jakarta"}`)},
		ThoughtSignature: "sig-abc",
	}})
	body, err := buildRequestBody(config.LLMBridge{}, []bridge.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", WireMeta: meta},
		{Role: "tool", Content: `{"temp":32}`},
	}, nil, requestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(body)
	if !strings.Contains(raw, "thoughtSignature") {
		t.Fatalf("missing thoughtSignature: %s", raw)
	}
	if !strings.Contains(raw, "functionResponse") {
		t.Fatal("missing functionResponse")
	}
}

func TestBridgeCompleteEmitsToolWithWireMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"echo","args":{},"id":"c1"},"thoughtSignature":"sig1"}],"role":"model"},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	streamOff := false
	entry := config.LLMBridge{
		Endpoint:       srv.URL,
		Model:          "test",
		CredentialsEnv: "GEMINI_API_KEY",
		Stream:         &streamOff,
	}
	t.Setenv("GEMINI_API_KEY", "test-key")

	b, err := New(entry)
	if err != nil {
		t.Fatal(err)
	}

	ch, err := b.Complete(context.Background(), bridge.Request{
		SessionID:     "s1",
		Messages:      []bridge.Message{{Role: "user", Content: "go"}},
		DeclaredTools: []string{"echo"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var sawTool bool
	for ev := range ch {
		if ev.Kind == bridge.EventToolCall {
			sawTool = true
			if ev.WireMeta == nil || ev.ToolCall == nil {
				t.Fatal("tool call missing wire meta or call")
			}
			if !strings.Contains(string(ev.WireMeta), "thoughtSignature") {
				t.Fatalf("wire meta missing signature: %s", ev.WireMeta)
			}
		}
	}
	if !sawTool {
		t.Fatal("expected tool call event")
	}
}

func TestNormalizeAPIBase(t *testing.T) {
	got := normalizeAPIBase("https://generativelanguage.googleapis.com/v1beta/models/m:generateContent")
	want := "https://generativelanguage.googleapis.com/v1beta"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDecodeWireMetaRejectsForeignDriver(t *testing.T) {
	raw, _ := json.Marshal(wireMetaPayload{Driver: "other", ModelParts: []part{{Text: "x"}}})
	if _, ok := decodeWireMeta(raw); ok {
		t.Fatal("expected reject foreign driver")
	}
}
