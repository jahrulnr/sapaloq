package ipc

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
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/core/orchestrator"
	"github.com/jahrulnr/sapaloq/internal/hostcontext"
)

type ipcTestHarness struct {
	orch   *orchestrator.Orchestrator
	socket string
	cancel context.CancelFunc
}

func startIPCTestHarness(t *testing.T) *ipcTestHarness {
	t.Helper()
	forceMockCursorCredentials(t)

	_, cfgPath, socket := config.WriteTestConfig(t, "ipc-test")
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
	orch, err := orchestrator.New(cfg, cfgPath, b, bus.New())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = NewServer(cfg, orch).ListenAndServe(ctx, socket) }()
	t.Cleanup(func() {
		cancel()
		time.Sleep(150 * time.Millisecond)
	})
	waitIPCServerSocket(t, socket)
	return &ipcTestHarness{orch: orch, socket: socket, cancel: cancel}
}

func forceMockCursorCredentials(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing-state.vscdb")
	t.Setenv("CURSOR_STATE_VSCDB", missing)
	t.Setenv("SAPALOQ_CURSOR_TOKEN", "")
	t.Setenv("CURSOR_ACCESS_TOKEN", "")
	t.Setenv("CURSOR_MACHINE_ID", "")
}

func waitIPCServerSocket(t *testing.T, socket string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("socket not ready: %s", socket)
}

func ipcRoundTrip(t *testing.T, socket string, req Request, untilDone bool) []Response {
	t.Helper()
	timeout := 5 * time.Second
	if untilDone {
		timeout = 30 * time.Second
	}
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", socket, err)
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
	var responses []Response
	for sc.Scan() {
		var res Response
		if err := json.Unmarshal(sc.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v line=%q", err, sc.Text())
		}
		responses = append(responses, res)
		if !untilDone {
			break
		}
		if res.Op == "event" && res.Event != nil && res.Event.Kind == bridge.EventTranscript &&
			res.Event.Transcript != nil && res.Event.Transcript.Finished {
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

func waitHostContextSnapshot(t *testing.T, orch *orchestrator.Orchestrator, sessionID string, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := orch.HostContextSnapshotPresent(sessionID)
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("host context snapshot present=%v want=%v session=%s", orch.HostContextSnapshotPresent(sessionID), want, sessionID)
}

func TestChatSendWithHostContext(t *testing.T) {
	h := startIPCTestHarness(t)
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "ipc-host-context"
	hostCtx, err := json.Marshal(hostcontext.Snapshot{
		Version: hostcontext.Version,
		Workspace: hostcontext.Workspace{
			SessionWorkspace: project,
		},
		Attachments: []hostcontext.Attachment{
			{Path: filepath.Join(project, "main.go"), Kind: "file", Name: "main.go"},
		},
		UI: hostcontext.UI{Mode: "orchestrator", ComposeAttachmentCount: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	responses := ipcRoundTrip(t, h.socket, Request{
		Op:          "chat_send",
		Message:     "hello host context",
		SessionID:   sessionID,
		HostContext: hostCtx,
	}, true)

	if len(responses) == 0 || !responses[0].OK || responses[0].Op != "chat_send" {
		t.Fatalf("chat_send accept failed: %+v", responses)
	}
	if responses[0].Message != "accepted" {
		t.Fatalf("message = %q", responses[0].Message)
	}
	waitHostContextSnapshot(t, h.orch, sessionID, true)
}

func TestChatSendInvalidHostContextStillAccepts(t *testing.T) {
	h := startIPCTestHarness(t)
	sessionID := "ipc-host-invalid"

	responses := ipcRoundTrip(t, h.socket, Request{
		Op:          "chat_send",
		Message:     "hello without host block",
		SessionID:   sessionID,
		HostContext: json.RawMessage(`{"version":99,"workspace":{"session_workspace":"/tmp"}}`),
	}, true)

	if len(responses) == 0 || !responses[0].OK || responses[0].Op != "chat_send" {
		t.Fatalf("chat_send accept failed: %+v", responses)
	}
	waitHostContextSnapshot(t, h.orch, sessionID, false)
}

func TestListenAndServeRejectsProductionSocketInGoTest(t *testing.T) {
	err := NewServer(config.Config{}, nil).ListenAndServe(context.Background(), config.ProductionSocketPath())
	if err == nil || !strings.Contains(err.Error(), "refusing to bind production IPC socket") {
		t.Fatalf("ListenAndServe() = %v, want production guard", err)
	}
}

func TestListenAndServePrivateModes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	socket := filepath.Join(root, config.TestSocketFileName)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NewServer(config.Config{}, nil).ListenAndServe(ctx, socket) }()
	for deadline := time.Now().Add(2 * time.Second); ; {
		if _, err := os.Lstat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket was not created")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if info, err := os.Stat(root); err != nil {
		t.Fatalf("runtime dir stat: %v", err)
	} else if info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime dir mode = %v", info.Mode().Perm())
	}
	if info, err := os.Lstat(socket); err != nil {
		t.Fatalf("socket stat: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v", info.Mode().Perm())
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("same-uid dial: %v", err)
	}
	_ = conn.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestListenRefusesNonSocketPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sapaloq.sock")
	if err := os.WriteFile(path, []byte("do not delete"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := NewServer(config.Config{}, nil).ListenAndServe(context.Background(), path)
	if err == nil {
		t.Fatal("expected non-socket refusal")
	}
	if got, _ := os.ReadFile(path); string(got) != "do not delete" {
		t.Fatalf("non-socket path was modified: %q", got)
	}
}

func TestListenRefusesActiveSocketWithoutUnlinkingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), config.TestSocketFileName)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	err = NewServer(config.Config{}, nil).ListenAndServe(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "already served") {
		t.Fatalf("ListenAndServe() = %v, want active-socket refusal", err)
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("original listener became unreachable: %v", err)
	}
	_ = conn.Close()
}

func TestListenReplacesStaleOwnedSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), config.TestSocketFileName)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NewServer(config.Config{}, nil).ListenAndServe(ctx, path) }()
	waitIPCServerSocket(t, path)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}
