package e2e_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/ipc"
)

func TestE2ELiveDoctor(t *testing.T) {
	requireLiveE2E(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := config.SaveRaw(cfgPath, map[string]any{
		"schemaVersion": "1.0.0",
		"runtime":       map[string]any{"dataDir": dir},
	}, "e2e-live"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	source, err := config.Doctor(cfg)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if source == "" {
		t.Fatal("expected credential source")
	}
	t.Logf("doctor credential source: %s", source)
}

func TestE2ELiveChatViaIPC(t *testing.T) {
	h := startLiveCore(t)
	responses := ipcRoundTripTimeout(t, h.SocketPath, ipc.Request{
		Op:        "chat_send",
		Message:   "Reply with exactly: pong",
		SessionID: "e2e-live-ipc",
	}, true, 2*time.Minute)

	assertLiveChatResult(t, responses)
}

func TestE2ELiveChatCLI(t *testing.T) {
	requireLiveE2E(t)
	if testing.Short() {
		t.Skip("subprocess live e2e skipped with -short")
	}

	bin := buildCoreBinary(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := config.SaveRaw(cfgPath, map[string]any{
		"schemaVersion": "1.0.0",
		"runtime":       map[string]any{"dataDir": dir},
	}, "e2e-live"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "chat", "Reply with exactly: pong")
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "SAPALOQ_CONFIG="+cfgPath)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil && !strings.Contains(text, "[error]") {
		t.Fatalf("chat cli: %v\n%s", err, text)
	}
	if strings.Contains(text, "offline mock stream") {
		t.Fatalf("fell back to mock stream:\n%s", text)
	}

	if strings.Contains(text, "[error]") {
		if strings.Contains(text, "[done]") {
			t.Fatalf("silent done after error:\n%s", text)
		}
		if liveE2EStrict() {
			t.Fatalf("live api error (strict mode):\n%s", text)
		}
		t.Logf("live cli surfaced error (expected until token works):\n%s", text)
		return
	}

	for _, want := range []string{"[response]", "[done]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestE2ELiveSubprocessCoreRun(t *testing.T) {
	requireLiveE2E(t)
	if testing.Short() {
		t.Skip("subprocess live e2e skipped with -short")
	}

	bin := buildCoreBinary(t)
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "run", "sapaloq.sock")
	cfgPath := filepath.Join(dir, "config.json")
	if err := config.SaveRaw(cfgPath, map[string]any{
		"schemaVersion": "1.0.0",
		"runtime":       map[string]any{"dataDir": dir},
		"events":        map[string]any{"bus": map[string]any{"socketPath": socketPath}},
	}, "e2e-live"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "run")
	cmd.Env = append(os.Environ(), "SAPALOQ_CONFIG="+cfgPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()
	})

	waitForSocket(t, socketPath)

	responses := ipcRoundTripTimeout(t, socketPath, ipc.Request{
		Op:        "chat_send",
		Message:   "Say hello in one short sentence.",
		SessionID: "e2e-live-subprocess",
	}, true, 2*time.Minute)
	assertLiveChatResult(t, responses)
}

// TestE2ELiveAgentAPI exercises the Agent API path against the real
// api5.cursor.sh endpoint. Set SAPALOQ_AGENT_PATH=1 to route the request
// through the agent.v1.AgentService/Run RPC instead of the legacy chat
// stream. Mirrors the JS reference at
// 9router/open-sse/executors/cursorAgent.js (agentn.global.api5.cursor.sh).
func TestE2ELiveAgentAPI(t *testing.T) {
	requireLiveE2E(t)
	h := startLiveCore(t)
	t.Setenv("SAPALOQ_AGENT_PATH", "1")
	responses := ipcRoundTripTimeout(t, h.SocketPath, ipc.Request{
		Op:        "chat_send",
		Message:   "Reply with exactly: agent-pong",
		SessionID: "e2e-live-agent",
	}, true, 2*time.Minute)
	assertLiveChatResult(t, responses)
}
