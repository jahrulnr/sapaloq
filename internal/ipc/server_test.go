package ipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
)

func TestListenAndServePrivateModes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	socket := filepath.Join(root, "sapaloq.sock")
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
