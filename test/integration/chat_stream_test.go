package integration_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/core/orchestrator"
)

func forceMockCredentials(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing-state.vscdb")
	t.Setenv("CURSOR_STATE_VSCDB", missing)
	t.Setenv("SAPALOQ_CURSOR_TOKEN", "")
	t.Setenv("CURSOR_ACCESS_TOKEN", "")
	t.Setenv("CURSOR_MACHINE_ID", "")
}

func TestChatStreamMockBridge(t *testing.T) {
	forceMockCredentials(t)
	cfg := config.DefaultConfig()
	reg := bridge.NewRegistry()
	if err := cursor.Register(reg, cfg); err != nil {
		t.Fatal(err)
	}
	b, err := reg.Get(cfg.LLMBridge.Driver)
	if err != nil {
		t.Fatal(err)
	}
	orch := orchestrator.New(cfg, "", b, bus.New())
	stream, err := orch.SendChat(context.Background(), "test", "hello mock")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[bridge.EventKind]bool{}
	for ev := range stream {
		seen[ev.Kind] = true
	}
	for _, kind := range []bridge.EventKind{bridge.EventThinkingDelta, bridge.EventResponseDelta, bridge.EventDone} {
		if !seen[kind] {
			t.Fatalf("missing %s", kind)
		}
	}
}

func TestSettingsPatchWritesConfig(t *testing.T) {
	forceMockCredentials(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.DefaultConfig()
	cfg.Runtime.DataDir = dir
	raw := map[string]any{
		"schemaVersion": "1.0.0",
		"notifications": map[string]any{"enabled": true, "read": false},
		"runtime":       map[string]any{"dataDir": dir},
	}
	if err := config.SaveRaw(cfgPath, raw, "test"); err != nil {
		t.Fatal(err)
	}

	reg := bridge.NewRegistry()
	if err := cursor.Register(reg, cfg); err != nil {
		t.Fatal(err)
	}
	b, err := reg.Get(cfg.LLMBridge.Driver)
	if err != nil {
		t.Fatal(err)
	}
	orch := orchestrator.New(cfg, cfgPath, b, bus.New())
	stream, err := orch.SendChat(context.Background(), "test", `/settings patch {"notifications":{"read":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	var response string
	for ev := range stream {
		if ev.Kind == bridge.EventResponseDelta {
			response = ev.Delta
		}
		if ev.Kind == bridge.EventError {
			t.Fatalf("error: %s", ev.Error)
		}
	}
	if response == "" {
		t.Fatal("expected settings response")
	}
	updated, err := config.LoadRaw(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	notifications, ok := updated["notifications"].(map[string]any)
	if !ok || notifications["read"] != true {
		b, _ := json.Marshal(updated["notifications"])
		t.Fatalf("notifications = %s", b)
	}
}
