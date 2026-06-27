package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/core/orchestrator"
	"github.com/jahrulnr/sapaloq/internal/ipc"
)

type coreHarness struct {
	SocketPath string
	ConfigPath string
	cancel     context.CancelFunc
	live       bool
}

type coreStartOptions struct {
	mock bool
}

func startInProcessCore(t *testing.T) *coreHarness {
	return startCore(t, coreStartOptions{mock: true})
}

func startLiveCore(t *testing.T) *coreHarness {
	requireLiveE2E(t)
	return startCore(t, coreStartOptions{mock: false})
}

func startCore(t *testing.T, opts coreStartOptions) *coreHarness {
	t.Helper()
	if opts.mock {
		forceMockCredentials(t)
	}

	dir := t.TempDir()
	socketPath := filepath.Join(dir, "run", "sapaloq.sock")
	cfgPath := filepath.Join(dir, "config.json")
	if err := config.SaveRaw(cfgPath, map[string]any{
		"schemaVersion": "1.0.0",
		"runtime":       map[string]any{"dataDir": dir},
		"events":        map[string]any{"bus": map[string]any{"socketPath": socketPath}},
	}, "e2e"); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.EnsureRuntimeDirs(config.RuntimeDirs(cfg)); err != nil {
		t.Fatal(err)
	}

	reg := bridge.NewRegistry()
	entry, err := cfg.LLMBridge.ActiveProvider()
	if err != nil {
		t.Fatal(err)
	}
	if err := cursor.Register(reg, entry, cfg.Runtime); err != nil {
		t.Fatal(err)
	}
	b, err := reg.Get(entry.Driver)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.mock && !b.Caps().LiveAPI {
		t.Fatal("expected live API bridge, got mock fallback (check credentials)")
	}

	orch, err := orchestrator.New(cfg, cfgPath, b, bus.New())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = ipc.NewServer(cfg, orch).ListenAndServe(ctx, socketPath)
	}()
	t.Cleanup(func() {
		cancel()
		time.Sleep(150 * time.Millisecond)
	})

	waitForSocket(t, socketPath)
	return &coreHarness{SocketPath: socketPath, ConfigPath: cfgPath, cancel: cancel, live: !opts.mock}
}

func forceMockCredentials(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing-state.vscdb")
	t.Setenv("CURSOR_STATE_VSCDB", missing)
	t.Setenv("SAPALOQ_CURSOR_TOKEN", "")
	t.Setenv("CURSOR_ACCESS_TOKEN", "")
	t.Setenv("CURSOR_MACHINE_ID", "")
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("socket not ready: %s", socketPath)
}

func ipcRoundTrip(t *testing.T, socketPath string, req ipc.Request, untilDone bool) []ipc.Response {
	return ipcRoundTripTimeout(t, socketPath, req, untilDone, 0)
}

func ipcRoundTripTimeout(t *testing.T, socketPath string, req ipc.Request, untilDone bool, timeout time.Duration) []ipc.Response {
	t.Helper()
	if timeout == 0 {
		timeout = 5 * time.Second
		if untilDone {
			timeout = 30 * time.Second
		}
	}
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", socketPath, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}

	sc := bufio.NewScanner(conn)
	var responses []ipc.Response
	for sc.Scan() {
		var res ipc.Response
		if err := json.Unmarshal(sc.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v line=%q", err, sc.Text())
		}
		responses = append(responses, res)
		if !untilDone {
			break
		}
		if res.Op == "event" && res.Event != nil && res.Event.Kind == bridge.EventTranscript && res.Event.Transcript != nil && res.Event.Transcript.Finished {
			break
		}
		if res.Op == "event" && res.Event != nil && res.Event.Kind == bridge.EventDone {
			break
		}
		if res.Op == "event" && res.Event != nil && res.Event.Kind == bridge.EventError {
			break
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return responses
}

func eventKinds(responses []ipc.Response) map[bridge.EventKind]int {
	seen := map[bridge.EventKind]int{}
	for _, res := range responses {
		if res.Event == nil {
			continue
		}
		seen[res.Event.Kind]++
	}
	return seen
}

func transcriptText(responses []ipc.Response) string {
	var out string
	for _, res := range responses {
		if res.Event == nil || res.Event.Transcript == nil {
			continue
		}
		for _, e := range res.Event.Transcript.Entries {
			if e.Kind != bridge.TranscriptText {
				continue
			}
			text := strings.TrimSpace(e.Text)
			if text == "" || strings.Contains(text, "<sapaloq:autopilot>") {
				continue
			}
			out = e.Text
		}
	}
	return out
}

func transcriptFinished(responses []ipc.Response) bool {
	for _, res := range responses {
		if res.Event != nil && res.Event.Kind == bridge.EventTranscript && res.Event.Transcript != nil && res.Event.Transcript.Finished {
			return true
		}
		if res.Event != nil && res.Event.Kind == bridge.EventDone {
			return true
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("go.mod not found")
		}
		wd = parent
	}
}

func requireLiveE2E(t *testing.T) credentials.Credentials {
	t.Helper()
	if !liveE2EEnabled() {
		t.Skip("set SAPALOQ_LIVE_E2E=1 to run live (non-mock) e2e against api2.cursor.sh")
	}
	creds, err := credentials.Load(credentials.Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN"})
	if err != nil {
		t.Skipf("live credentials unavailable: %v", err)
	}
	t.Logf("live creds: source=%s token=%s machine=%s",
		creds.Source, redact(creds.AccessToken), redact(creds.MachineID))
	return creds
}

func liveE2EEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SAPALOQ_LIVE_E2E")))
	return v == "1" || v == "true" || v == "yes"
}

func liveE2EStrict() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SAPALOQ_LIVE_E2E_STRICT")))
	return v == "1" || v == "true" || v == "yes"
}

func redact(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 8 {
		return "…"
	}
	return s[:4] + "…" + s[len(s)-4:]
}

func isMockStream(responses []ipc.Response) bool {
	for _, res := range responses {
		if res.Event == nil {
			continue
		}
		if res.Event.Kind == bridge.EventThinkingDelta && strings.Contains(res.Event.Delta, "offline mock stream") {
			return true
		}
		if res.Event.Kind == bridge.EventTranscript && res.Event.Transcript != nil {
			for _, e := range res.Event.Transcript.Entries {
				if e.Kind == bridge.TranscriptThinking && strings.Contains(e.Text, "offline mock stream") {
					return true
				}
			}
		}
	}
	return false
}

func firstStreamError(responses []ipc.Response) string {
	for _, res := range responses {
		if res.Event != nil && res.Event.Kind == bridge.EventError {
			return res.Event.Error
		}
		if !res.OK && res.Message != "" {
			return res.Message
		}
	}
	return ""
}

func assertLiveChatResult(t *testing.T, responses []ipc.Response) {
	t.Helper()
	if isMockStream(responses) {
		t.Fatal("fell back to mock stream - live credentials not used")
	}
	seen := eventKinds(responses)
	if seen[bridge.EventError] > 0 {
		errText := firstStreamError(responses)
		if transcriptFinished(responses) {
			t.Fatalf("live stream returned finished transcript after error: %s", errText)
		}
		if liveE2EStrict() {
			t.Fatalf("live api error (strict mode): %s", errText)
		}
		t.Logf("live api returned error (surfaced correctly, not silent done): %s", errText)
		return
	}
	if seen[bridge.EventTranscript] == 0 || !transcriptFinished(responses) {
		t.Fatalf("live chat missing finished transcript (seen=%v)", seen)
	}
}
