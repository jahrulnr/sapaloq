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

// activeEntry resolves the active provider entry + runtime config from
// the loaded Config. Used by tests that need to call bridge constructors.
func activeEntry(t *testing.T, cfg config.Config) (config.LLMBridge, config.RuntimeConfig) {
	t.Helper()
	entry, err := cfg.LLMBridge.ActiveProvider()
	if err != nil {
		t.Fatal(err)
	}
	return entry, cfg.Runtime
}

func TestChatStreamMockBridge(t *testing.T) {
	forceMockCredentials(t)
	_, cfgPath, _ := config.WriteTestConfig(t, "integration-mock-chat")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	reg := bridge.NewRegistry()
	entry, runtime := activeEntry(t, cfg)
	if err := cursor.Register(reg, entry, runtime); err != nil {
		t.Fatal(err)
	}
	b, err := reg.Get(entry.Driver)
	if err != nil {
		t.Fatal(err)
	}
	orch, err := orchestrator.New(cfg, "", b, bus.New())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := orch.SendChat(context.Background(), "test", "hello mock", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[bridge.EventKind]bool{}
	var hasText bool
	for ev := range stream {
		seen[ev.Kind] = true
		if ev.Kind == bridge.EventTranscript && ev.Transcript != nil {
			for _, e := range ev.Transcript.Entries {
				if e.Kind == bridge.TranscriptText {
					hasText = true
				}
			}
		}
	}
	if !seen[bridge.EventTranscript] && !seen[bridge.EventDone] {
		t.Fatalf("missing transcript stream, seen=%v", seen)
	}
	if !hasText {
		t.Fatal("missing text in transcript")
	}
}

func TestSettingsPatchWritesConfig(t *testing.T) {
	forceMockCredentials(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.DefaultConfig()
	cfg.Runtime.DataDir = dir
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	raw["orchestrator"] = map[string]any{
		"completion": map[string]any{"notifyUserOnDone": false},
	}
	if err := config.SaveRaw(cfgPath, raw, "test"); err != nil {
		t.Fatal(err)
	}

	reg := bridge.NewRegistry()
	entry, runtime := activeEntry(t, cfg)
	if err := cursor.Register(reg, entry, runtime); err != nil {
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
	stream, err := orch.SendChat(context.Background(), "test", `/settings patch {"orchestrator":{"completion":{"notifyUserOnDone":true}}}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var response string
	for ev := range stream {
		if ev.Kind == bridge.EventResponseDelta {
			response = ev.Delta
		}
		if ev.Kind == bridge.EventTranscript && ev.Transcript != nil {
			for _, e := range ev.Transcript.Entries {
				if e.Kind == bridge.TranscriptText {
					response = e.Text
				}
			}
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
	orchestratorCfg, ok := updated["orchestrator"].(map[string]any)
	if !ok {
		t.Fatalf("orchestrator = %#v", updated["orchestrator"])
	}
	completion, ok := orchestratorCfg["completion"].(map[string]any)
	if !ok || completion["notifyUserOnDone"] != true {
		b, _ := json.Marshal(orchestratorCfg["completion"])
		t.Fatalf("completion = %s", b)
	}
}
