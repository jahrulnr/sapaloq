package e2e_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	if seen[bridge.EventTranscript] == 0 {
		t.Fatalf("missing transcript (seen=%v)", seen)
	}
	if !transcriptFinished(responses) {
		t.Fatalf("transcript never finished (seen=%v)", seen)
	}

	responseText := transcriptText(responses)
	if responseText == "" {
		t.Fatalf("empty transcript response")
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
	if seen[bridge.EventTranscript] == 0 {
		t.Fatalf("missing transcript (seen=%v)", seen)
	}
	for _, res := range responses {
		if res.Event == nil || res.Event.Transcript == nil {
			continue
		}
		for _, e := range res.Event.Transcript.Entries {
			if e.Kind == bridge.TranscriptTool && e.ToolName == "glob_file_search" {
				return
			}
		}
	}
	t.Fatal("transcript missing glob_file_search tool")
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
	raw, err := config.LoadRaw(h.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	raw["orchestrator"] = map[string]any{
		"completion": map[string]any{"notifyUserOnDone": false},
	}
	if err := config.SaveRaw(h.ConfigPath, raw, "e2e-setup"); err != nil {
		t.Fatal(err)
	}

	responses := ipcRoundTrip(t, h.SocketPath, ipc.Request{
		Op:        "chat_send",
		Message:   `/settings patch {"orchestrator":{"completion":{"notifyUserOnDone":true}}}`,
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

	raw, err = config.LoadRaw(h.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	orchestratorCfg, ok := raw["orchestrator"].(map[string]any)
	if !ok {
		t.Fatalf("orchestrator = %#v", raw["orchestrator"])
	}
	completion, ok := orchestratorCfg["completion"].(map[string]any)
	if !ok || completion["notifyUserOnDone"] != true {
		t.Fatalf("completion = %#v", orchestratorCfg["completion"])
	}
}

func TestE2EWatchRehydratesLiveTaskStatus(t *testing.T) {
	h := startInProcessCore(t)
	taskID := "task-watch-catchup"
	taskDir := filepath.Join(filepath.Dir(h.ConfigPath), "state", "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	raw, err := json.Marshal(map[string]any{
		"id":         taskID,
		"session_id": "watch-session",
		"role":       "task-runner",
		"status":     "in_progress",
		"task":       "build profile",
		"created_at": now,
		"updated_at": now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "status.json"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	conn, err := net.DialTimeout("unix", h.SocketPath, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(ipc.Request{Op: "watch"})
	if _, err := conn.Write(append(request, '\n')); err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(conn)
	var responses []ipc.Response
	for len(responses) < 2 && scanner.Scan() {
		var response ipc.Response
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(responses) != 2 || responses[0].Op != "watch" {
		t.Fatalf("watch handshake/snapshot missing: %+v", responses)
	}
	event := responses[1].Event
	if event == nil || event.Kind != bridge.EventTaskUpdate || event.TaskID != taskID || event.TaskStatus != "in_progress" {
		t.Fatalf("live task snapshot not delivered: %+v", responses[1])
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
