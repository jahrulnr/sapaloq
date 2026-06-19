package e2e_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/ipc"
)

func TestE2EPing(t *testing.T) {
	h := startInProcessCore(t)
	responses := ipcRoundTrip(t, h.SocketPath, ipc.Request{Op: "ping"}, false)
	if len(responses) == 0 || !responses[0].OK {
		t.Fatalf("ping failed: %+v", responses)
	}
	if responses[0].Message != "pong" {
		t.Fatalf("message = %q", responses[0].Message)
	}
	if responses[0].RingState != "idle" {
		t.Fatalf("ring_state = %q", responses[0].RingState)
	}
}

func TestE2EChatMockStream(t *testing.T) {
	h := startInProcessCore(t)
	responses := ipcRoundTrip(t, h.SocketPath, ipc.Request{
		Op:        "chat_send",
		Message:   "hello e2e",
		SessionID: "e2e-chat",
	}, true)

	if len(responses) == 0 || !responses[0].OK || responses[0].Op != "chat_send" {
		t.Fatalf("accept failed: %+v", firstN(responses, 2))
	}

	seen := eventKinds(responses)
	for _, kind := range []bridge.EventKind{
		bridge.EventThinkingDelta,
		bridge.EventResponseDelta,
		bridge.EventDone,
	} {
		if seen[kind] == 0 {
			t.Fatalf("missing %s (seen=%v)", kind, seen)
		}
	}

	var responseText string
	for _, res := range responses {
		if res.Event != nil && res.Event.Kind == bridge.EventResponseDelta {
			responseText += res.Event.Delta
		}
	}
	if !strings.Contains(responseText, "hello e2e") {
		t.Fatalf("response = %q", responseText)
	}
}

func TestE2EChatToolCoerce(t *testing.T) {
	h := startInProcessCore(t)
	responses := ipcRoundTrip(t, h.SocketPath, ipc.Request{
		Op:        "chat_send",
		Message:   "please use glob tool",
		SessionID: "e2e-tool",
	}, true)

	seen := eventKinds(responses)
	if seen[bridge.EventToolCall] == 0 {
		t.Fatalf("missing tool_call (seen=%v)", seen)
	}
	for _, res := range responses {
		if res.Event != nil && res.Event.Kind == bridge.EventToolCall && res.Event.ToolCall != nil {
			if res.Event.ToolCall.Name != "glob_file_search" {
				t.Fatalf("tool = %q", res.Event.ToolCall.Name)
			}
			return
		}
	}
	t.Fatal("tool_call event without payload")
}

func TestE2ESlashSuggest(t *testing.T) {
	h := startInProcessCore(t)
	responses := ipcRoundTrip(t, h.SocketPath, ipc.Request{
		Op:    "slash_suggest",
		Query: "/set",
	}, false)
	if len(responses) == 0 || !responses[0].OK {
		t.Fatalf("slash_suggest failed: %+v", responses)
	}
	if len(responses[0].Suggestions) == 0 {
		t.Fatal("expected suggestions")
	}
	if responses[0].Suggestions[0].ID != "settings" {
		t.Fatalf("suggestion = %+v", responses[0].Suggestions[0])
	}
}

func TestE2ESettingsPatchViaIPC(t *testing.T) {
	h := startInProcessCore(t)
	if err := config.SaveRaw(h.ConfigPath, map[string]any{
		"schemaVersion": "1.0.0",
		"notifications": map[string]any{"enabled": true, "read": false},
		"runtime":       map[string]any{"dataDir": filepath.Dir(h.ConfigPath)},
		"events":        map[string]any{"bus": map[string]any{"socketPath": h.SocketPath}},
	}, "e2e-setup"); err != nil {
		t.Fatal(err)
	}

	responses := ipcRoundTrip(t, h.SocketPath, ipc.Request{
		Op:        "chat_send",
		Message:   `/settings patch {"notifications":{"read":true}}`,
		SessionID: "e2e-settings",
	}, true)

	seen := eventKinds(responses)
	if seen[bridge.EventError] > 0 {
		for _, res := range responses {
			if res.Event != nil && res.Event.Kind == bridge.EventError {
				t.Fatalf("settings error: %s", res.Event.Error)
			}
		}
	}
	if seen[bridge.EventResponseDelta] == 0 {
		t.Fatalf("missing response (seen=%v)", seen)
	}

	raw, err := config.LoadRaw(h.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	notifications, ok := raw["notifications"].(map[string]any)
	if !ok || notifications["read"] != true {
		t.Fatalf("notifications = %#v", raw["notifications"])
	}
}

func TestE2EUnknownOp(t *testing.T) {
	h := startInProcessCore(t)
	responses := ipcRoundTrip(t, h.SocketPath, ipc.Request{Op: "nope"}, false)
	if len(responses) == 0 || responses[0].OK {
		t.Fatalf("expected failure: %+v", responses)
	}
}

func firstN[T any](items []T, n int) []T {
	if len(items) <= n {
		return items
	}
	return items[:n]
}
