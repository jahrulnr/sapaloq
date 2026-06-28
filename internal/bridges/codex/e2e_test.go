//go:build e2e

package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/codex/appserver"
	"github.com/jahrulnr/sapaloq/internal/config"
)

func requireCodex(t *testing.T) string {
	t.Helper()
	bin, err := resolveBinary()
	if err != nil {
		t.Skipf("codex binary not found: %v", err)
	}
	return bin
}

func TestE2EAppServerLifecycle(t *testing.T) {
	bin := requireCodex(t)
	endpoint := "unix://" + filepath.Join(t.TempDir(), "codex.sock")
	m := &appserver.Manager{Binary: bin, Endpoint: endpoint, Mode: appserver.ModeAuto, Env: os.Environ()}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.EnsureRunning(ctx); err != nil {
		t.Fatalf("start/probe: %v", err)
	}
	if !m.SpawnedByUs() {
		t.Fatal("expected lifecycle manager to own the child")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("reap: %v", err)
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer probeCancel()
	if err := appserver.Probe(probeCtx, endpoint); err == nil {
		t.Fatal("app-server remained reachable after Close")
	}
}

func TestE2EAppServerTurn(t *testing.T) {
	if os.Getenv("SAPALOQ_CODEX_E2E") != "1" {
		t.Skip("set SAPALOQ_CODEX_E2E=1 to run a live model turn")
	}
	bin := requireCodex(t)
	t.Setenv(envBinary, bin)
	t.Setenv(envMode, appserver.ModeAuto)
	t.Setenv(envListen, "unix://"+filepath.Join(t.TempDir(), "codex.sock"))
	b, err := New(config.LLMBridge{Driver: "codex-bridge", Model: "gpt-5.5", RequestTimeoutSec: 120}, config.RuntimeConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	stream, err := b.Complete(ctx, bridge.Request{SessionID: "e2e-pong", Messages: []bridge.Message{{Role: "user", Content: "Reply with exactly PONG."}}})
	if err != nil {
		t.Fatal(err)
	}
	var response, streamErr string
	var done bool
	for ev := range stream {
		switch ev.Kind {
		case bridge.EventResponseDelta:
			response += ev.Delta
		case bridge.EventError:
			streamErr = ev.Error
		case bridge.EventDone:
			done = true
		}
	}
	if streamErr != "" {
		if isCodexUsageLimitError(streamErr) {
			t.Skipf("codex usage limit: %s", streamErr)
		}
		t.Fatalf("turn error: %s", streamErr)
	}
	if !done || !strings.Contains(strings.ToUpper(response), "PONG") {
		t.Fatalf("response=%q done=%v", response, done)
	}

	stream, err = b.Complete(ctx, bridge.Request{SessionID: "e2e-pong", Messages: []bridge.Message{
		{Role: "user", Content: "Reply with exactly PONG."},
		{Role: "assistant", Content: response},
		{Role: "user", Content: "Reply with exactly SECOND."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	response, streamErr, done = "", "", false
	for ev := range stream {
		switch ev.Kind {
		case bridge.EventResponseDelta:
			response += ev.Delta
		case bridge.EventError:
			streamErr = ev.Error
		case bridge.EventDone:
			done = true
		}
	}
	if streamErr != "" {
		if isCodexUsageLimitError(streamErr) {
			t.Skipf("codex usage limit on resume turn: %s", streamErr)
		}
	}
	if streamErr != "" || !done || !strings.Contains(strings.ToUpper(response), "SECOND") {
		t.Fatalf("resume response=%q error=%q done=%v", response, streamErr, done)
	}
}

func isCodexUsageLimitError(msg string) bool {
	l := strings.ToLower(msg)
	return strings.Contains(l, "usage limit") ||
		strings.Contains(l, "rate limit") ||
		strings.Contains(l, "try again at")
}
