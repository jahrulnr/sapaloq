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

func buildCoreBinary(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "sapaloq-core")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/sapaloq-core")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build sapaloq-core: %v\n%s", err, out)
	}
	return bin
}

func TestE2ESubprocessCoreRun(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess e2e skipped with -short")
	}

	forceMockCredentials(t)
	bin := buildCoreBinary(t)
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

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "run")
	cmd.Env = append(os.Environ(),
		"SAPALOQ_CONFIG="+cfgPath,
		"CURSOR_STATE_VSCDB="+filepath.Join(dir, "missing.vscdb"),
		"SAPALOQ_CURSOR_TOKEN=",
		"CURSOR_ACCESS_TOKEN=",
		"CURSOR_MACHINE_ID=",
	)
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

	ping := ipcRoundTrip(t, socketPath, ipc.Request{Op: "ping"}, false)
	if len(ping) == 0 || !ping[0].OK {
		t.Fatalf("subprocess ping failed: %+v", ping)
	}

	chat := ipcRoundTrip(t, socketPath, ipc.Request{
		Op:        "chat_send",
		Message:   "subprocess hello",
		SessionID: "e2e-subprocess",
	}, true)
	seen := eventKinds(chat)
	if !transcriptFinished(chat) {
		t.Fatalf("subprocess chat missing finished transcript (seen=%v)", seen)
	}

	response := transcriptText(chat)
	if !strings.Contains(response, "subprocess hello") {
		t.Fatalf("response = %q", response)
	}
}

func TestE2ESubprocessChatCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess e2e skipped with -short")
	}

	forceMockCredentials(t)
	bin := buildCoreBinary(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := config.SaveRaw(cfgPath, map[string]any{
		"schemaVersion": "1.0.0",
		"runtime":       map[string]any{"dataDir": dir},
	}, "e2e"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "chat", "cli e2e ping")
	cmd.Env = append(os.Environ(),
		"SAPALOQ_CONFIG="+cfgPath,
		"CURSOR_STATE_VSCDB="+filepath.Join(dir, "missing.vscdb"),
		"SAPALOQ_CURSOR_TOKEN=",
		"CURSOR_ACCESS_TOKEN=",
		"CURSOR_MACHINE_ID=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chat cli: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{"[thinking]", "[response]", "[done]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "cli e2e ping") {
		t.Fatalf("output missing message:\n%s", text)
	}
}
