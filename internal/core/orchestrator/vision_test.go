package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestImageRejectionDetection(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"explicit-not-support", "this model does not support image input", true},
		{"explicit-text-only", "model is text-only", true},
		{"multimodal-phrase", "endpoint is not multimodal", true},
		{"status-400-plain", "provider-bridge: upstream status 400: invalid 'messages': image not allowed", true},
		{"status-400-generic", "provider-bridge: upstream status 400: bad request", true},
		{"status-400-rate-limit", "provider-bridge: upstream status 400: rate limit exceeded", false},
		{"status-429", "provider-bridge: upstream status 429: too many requests", false},
		{"status-400-context-length", "provider-bridge: upstream status 400: maximum context length exceeded", false},
		{"status-400-billing", "provider-bridge: upstream status 400: billing hard limit reached", false},
		{"status-401-auth", "provider-bridge: upstream status 401: invalid api key", false},
		{"status-500", "provider-bridge: upstream status 500: server error", false},
		{"unrelated", "connection reset by peer", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageRejection(tc.msg); got != tc.want {
				t.Fatalf("imageRejection(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

// visionErrorBridge returns a vision-rejection error on its first call and a
// plain text answer on every subsequent call. It records each request so the
// test can assert the retry dropped the image.
type visionErrorBridge struct {
	requests []bridge.Request
}

func (b *visionErrorBridge) ID() string              { return "vision-error" }
func (b *visionErrorBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *visionErrorBridge) Complete(_ context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.requests = append(b.requests, req)
	call := len(b.requests)
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		if call == 1 {
			out <- bridge.StreamEvent{Kind: bridge.EventError, Error: "provider-bridge: upstream status 400: this model does not support image input"}
		} else {
			out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "text-only answer"}
			stop := parse.ToolCall{Name: "sapaloq_stop", Arguments: []byte(`{"reason":"done"}`)}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &stop}
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

func TestRunConversationRetriesWithoutImagesOnVisionReject(t *testing.T) {
	fake := &visionErrorBridge{}
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "test", Model: "model"}, br: fake}
	out := make(chan bridge.StreamEvent, 32)
	go func() {
		for range out {
		}
	}()

	img := "data:image/png;base64,iVBORw0KGgo="
	messages := []bridge.Message{{Role: "user", Content: "look at this ![pic](" + img + ")"}}
	result, err := orch.runConversation(context.Background(), snap, out, "session", "task", messages, nil)
	close(out)
	if err != nil {
		t.Fatalf("expected graceful retry, got error: %v", err)
	}
	if result.String() != "text-only answer" {
		t.Fatalf("answer = %q, want text-only answer", result.String())
	}
	if len(fake.requests) != 2 {
		t.Fatalf("expected 2 upstream calls (image then text-only), got %d", len(fake.requests))
	}
	if len(fake.requests[0].Images) != 1 {
		t.Fatalf("first call should carry the image, got %d", len(fake.requests[0].Images))
	}
	if len(fake.requests[1].Images) != 0 {
		t.Fatalf("retry must drop images, got %d", len(fake.requests[1].Images))
	}
	if orch.visionAllowed("test", "model") {
		t.Fatalf("model should be marked text-only after a vision rejection")
	}
}

func TestSeedVisionFromConfig(t *testing.T) {
	no := false
	yes := true
	orch := &Orchestrator{vision: make(map[string]bool)}
	cfg := config.Config{}
	cfg.LLMBridge.Providers = []config.LLMBridge{
		{Key: "blind", Model: "m1", SupportsImages: &no},
		{Key: "seer", Model: "m2", SupportsImages: &yes},
		{Key: "unknown", Model: "m3"},
	}
	orch.seedVisionFromConfig(cfg)

	if orch.visionAllowed("blind", "m1") {
		t.Fatalf("m1 marked supportsImages:false should not be allowed")
	}
	if !orch.visionAllowed("seer", "m2") {
		t.Fatalf("m2 marked supportsImages:true should be allowed")
	}
	if !orch.visionAllowed("unknown", "m3") {
		t.Fatalf("m3 with nil supportsImages should default to allowed (try it)")
	}
}

func TestPersistVisionSupportWritesConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	raw := map[string]any{
		"llmBridge": map[string]any{
			"providerKey": "blind",
			"providers": []any{
				map[string]any{"key": "blind", "model": "m1", "driver": "provider-bridge"},
				map[string]any{"key": "other", "model": "m2", "driver": "provider-bridge"},
			},
		},
	}
	b, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		t.Fatal(err)
	}

	orch := &Orchestrator{cfgPath: cfgPath, vision: make(map[string]bool)}
	orch.persistVisionSupport("blind", "m1", false)

	out, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	providers := got["llmBridge"].(map[string]any)["providers"].([]any)
	first := providers[0].(map[string]any)
	if v, ok := first["supportsImages"].(bool); !ok || v != false {
		t.Fatalf("provider 'blind' should have supportsImages:false, got %v (raw: %s)", first["supportsImages"], string(out))
	}
	second := providers[1].(map[string]any)
	if _, ok := second["supportsImages"]; ok {
		t.Fatalf("provider 'other' must be untouched, got supportsImages=%v", second["supportsImages"])
	}
	if !strings.Contains(string(out), "orchestrator:vision-probe") {
		t.Fatalf("expected updatedBy stamp from SaveRaw, got: %s", string(out))
	}
}

func TestPersistVisionSupportMatchesByModelWhenKeyEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	raw := map[string]any{
		"llmBridge": map[string]any{
			"providers": []any{
				map[string]any{"model": "gpt-text", "driver": "provider-bridge"},
			},
		},
	}
	b, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	orch := &Orchestrator{cfgPath: cfgPath, vision: make(map[string]bool)}
	orch.persistVisionSupport("", "gpt-text", false)

	out, _ := os.ReadFile(cfgPath)
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	entry := got["llmBridge"].(map[string]any)["providers"].([]any)[0].(map[string]any)
	if v, ok := entry["supportsImages"].(bool); !ok || v != false {
		t.Fatalf("model-matched entry should have supportsImages:false, got %v", entry["supportsImages"])
	}
}
