package appserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_CODEX_APPSERVER_HELPER") == "1" {
		runProcessHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestManagerAutoSpawnsAndReaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.sock")
	m := &Manager{
		Binary: os.Args[0], Endpoint: "unix://" + path, Mode: ModeAuto,
		Env: append(os.Environ(), "GO_WANT_CODEX_APPSERVER_HELPER=1"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.EnsureRunning(ctx); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if !m.SpawnedByUs() {
		t.Fatal("manager did not record process ownership")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer probeCancel()
	if err := Probe(probeCtx, "unix://"+path); err == nil {
		t.Fatal("app-server still reachable after reap")
	}
}

func TestManagerExternalNeverSpawns(t *testing.T) {
	m := &Manager{Binary: os.Args[0], Endpoint: "unix://" + filepath.Join(t.TempDir(), "missing.sock"), Mode: ModeExternal}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := m.EnsureRunning(ctx); err == nil {
		t.Fatal("external mode should fail when endpoint is absent")
	}
	if m.SpawnedByUs() {
		t.Fatal("external mode spawned a child")
	}
}

func runProcessHelper() {
	var endpoint string
	for i, arg := range os.Args {
		if arg == "--listen" && i+1 < len(os.Args) {
			endpoint = os.Args[i+1]
		}
	}
	path := strings.TrimPrefix(endpoint, "unix://")
	listener, err := net.Listen("unix", path)
	if err != nil {
		os.Exit(2)
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var req wireMessage
			if conn.ReadJSON(&req) != nil {
				return
			}
			if req.Method == "initialize" {
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
			}
		}
	})
	_ = http.Serve(listener, handler)
}
