package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	provider "github.com/jahrulnr/sapaloq/internal/bridges/provider"
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
	}, nil, requestOptions{}, nil)
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

func TestBuildFunctionDeclarationsNoAdditionalProperties(t *testing.T) {
	// Register a tool with additionalProperties in its schema to verify stripping
	provider.RegisterTool("test_strip_tool", json.RawMessage(`{
		"type":"object",
		"additionalProperties":true,
		"properties":{
			"nested":{"type":"object","additionalProperties":true,"properties":{"x":{"type":"string"}}}
		}
	}`), "test")

	decls := buildFunctionDeclarations([]string{"test_strip_tool", "unregistered_tool"})
	for _, decl := range decls {
		params, ok := decl["parameters"].(map[string]any)
		if !ok {
			t.Fatalf("parameters not a map: %v", decl["parameters"])
		}
		if _, has := params["additionalProperties"]; has {
			t.Fatalf("additionalProperties present in params for %q: %v", decl["name"], params)
		}
		if props, ok := params["properties"].(map[string]any); ok {
			for k, v := range props {
				if child, ok := v.(map[string]any); ok {
					if _, has := child["additionalProperties"]; has {
						t.Fatalf("additionalProperties present in nested property %q: %v", k, child)
					}
				}
			}
		}
	}
}

func TestStripAdditionalPropertiesHandlesItems(t *testing.T) {
	provider.RegisterTool("test_items_tool", json.RawMessage(`{
		"type":"object",
		"additionalProperties":true,
		"properties":{
			"tags":{"type":"array","items":{"type":"object","additionalProperties":true,"properties":{"name":{"type":"string"}}}}
		}
	}`), "test")

	decls := buildFunctionDeclarations([]string{"test_items_tool"})
	for _, decl := range decls {
		params, ok := decl["parameters"].(map[string]any)
		if !ok {
			t.Fatalf("parameters not a map: %v", decl["parameters"])
		}
		if _, has := params["additionalProperties"]; has {
			t.Fatal("additionalProperties present at top level")
		}
		props, _ := params["properties"].(map[string]any)
		tags, _ := props["tags"].(map[string]any)
		items, _ := tags["items"].(map[string]any)
		if _, has := items["additionalProperties"]; has {
			t.Fatal("additionalProperties present in items")
		}
	}
}

func TestImagesAttachedAsInlineData(t *testing.T) {
	images := []bridge.Image{
		{DataURI: "data:image/png;base64,iVBORw0KGgo="},
	}
	body, err := buildRequestBody(config.LLMBridge{}, []bridge.Message{
		{Role: "user", Content: "what is this?"},
	}, nil, requestOptions{}, images)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(body)
	if !strings.Contains(raw, "inlineData") {
		t.Fatalf("missing inlineData in request body: %s", raw)
	}
	if !strings.Contains(raw, "image/png") {
		t.Fatalf("missing mimeType in inlineData: %s", raw)
	}
	if !strings.Contains(raw, "iVBORw0KGgo=") {
		t.Fatalf("missing base64 data in inlineData: %s", raw)
	}
}

func TestImagesAttachedToLastUserMessage(t *testing.T) {
	images := []bridge.Image{
		{DataURI: "data:image/jpeg;base64,abc123"},
	}
	body, err := buildRequestBody(config.LLMBridge{}, []bridge.Message{
		{Role: "user", Content: "first message"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "second message with image"},
	}, nil, requestOptions{}, images)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	contents := payload["contents"].([]any)
	// Last content should be the user message with inlineData
	last := contents[len(contents)-1].(map[string]any)
	if last["role"] != "user" {
		t.Fatalf("last content role = %v, want user", last["role"])
	}
	parts := last["parts"].([]any)
	hasInline := false
	for _, p := range parts {
		pm := p.(map[string]any)
		if _, ok := pm["inlineData"]; ok {
			hasInline = true
			break
		}
	}
	if !hasInline {
		t.Fatal("last user message missing inlineData part")
	}
}

func TestNoImagesNoInlineData(t *testing.T) {
	body, err := buildRequestBody(config.LLMBridge{}, []bridge.Message{
		{Role: "user", Content: "text only"},
	}, nil, requestOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "inlineData") {
		t.Fatalf("inlineData should not be present without images: %s", body)
	}
}
