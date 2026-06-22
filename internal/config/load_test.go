package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLLMBridgeRootValidate(t *testing.T) {
	cases := []struct {
		name    string
		root    LLMBridgeRoot
		wantErr string // substring; empty means no error
	}{
		{
			name: "valid cursor",
			root: LLMBridgeRoot{
				ProviderKey: "cursor",
				Providers: []LLMBridge{
					{Key: "cursor", Driver: "cursor-bridge", Endpoint: "https://x", Model: "m", CredentialsEnv: "E"},
				},
			},
		},
		{
			name: "valid multi-provider",
			root: LLMBridgeRoot{
				ProviderKey: "openai",
				Providers: []LLMBridge{
					{Key: "cursor", Driver: "cursor-bridge", Endpoint: "https://x", Model: "m", CredentialsEnv: "E"},
					{Key: "openai", Driver: "provider-bridge", Endpoint: "https://y", Model: "gpt", CredentialsEnv: "O"},
					{Key: "claude", Driver: "provider-bridge", Endpoint: "https://z", Model: "c", CredentialsEnv: "A"},
				},
			},
		},
		{
			name:    "missing providerKey",
			root:    LLMBridgeRoot{Providers: []LLMBridge{{Key: "a", Driver: "x"}}},
			wantErr: "providerKey is required",
		},
		{
			name:    "empty providers",
			root:    LLMBridgeRoot{ProviderKey: "x"},
			wantErr: "must contain at least one entry",
		},
		{
			name: "empty entry key",
			root: LLMBridgeRoot{
				ProviderKey: "x",
				Providers:   []LLMBridge{{Driver: "y"}},
			},
			wantErr: "key is required",
		},
		{
			name: "duplicate keys",
			root: LLMBridgeRoot{
				ProviderKey: "a",
				Providers: []LLMBridge{
					{Key: "a", Driver: "x", Endpoint: "e", Model: "m", CredentialsEnv: "E"},
					{Key: "a", Driver: "y", Endpoint: "f", Model: "n", CredentialsEnv: "F"},
				},
			},
			wantErr: "duplicate key",
		},
		{
			name: "providerKey not in array",
			root: LLMBridgeRoot{
				ProviderKey: "missing",
				Providers: []LLMBridge{
					{Key: "a", Driver: "x", Endpoint: "e", Model: "m", CredentialsEnv: "E"},
				},
			},
			wantErr: "does not match any entry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.root.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestLoadBootstrapsConfigFromWorkspaceExample(t *testing.T) {
	workspace := t.TempDir()
	exampleDir := filepath.Join(workspace, "sapaloq", "config")
	if err := os.MkdirAll(exampleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	example := `{"schemaVersion":"1.0.0","runtime":{"dataDir":"` + filepath.ToSlash(filepath.Join(workspace, "data")) + `"},"llmBridge":{"providerKey":"cursor","providers":[{"key":"cursor","driver":"cursor-bridge","endpoint":"https://api2.cursor.sh","model":"default","credentialsEnv":"SAPALOQ_CURSOR_TOKEN"}]},"events":{"bus":{"socketPath":"` + filepath.ToSlash(filepath.Join(workspace, "run", "sapaloq.sock")) + `"}}}`
	if err := os.WriteFile(filepath.Join(exampleDir, "config.example.json"), []byte(example), 0o644); err != nil {
		t.Fatal(err)
	}
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	path := filepath.Join(workspace, "data", "config.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config.json bootstrap: %v", err)
	}
	if cfg.Runtime.DataDir != filepath.Join(workspace, "data") {
		t.Fatalf("unexpected data dir: %q", cfg.Runtime.DataDir)
	}
}

func TestDefaultConfigPathIsSeparateFromRuntimeData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := DefaultConfig()
	if got, want := ConfigPath("", cfg), filepath.Join(home, ".config", "sapaloq", "config.json"); got != want {
		t.Fatalf("ConfigPath = %q, want %q", got, want)
	}
	if got, want := RuntimeDirs(cfg).DataDir, filepath.Join(home, "SapaLOQ"); got != want {
		t.Fatalf("DataDir = %q, want %q", got, want)
	}
}

func TestLLMBridgeRootActiveProvider(t *testing.T) {
	root := LLMBridgeRoot{
		ProviderKey: "openai",
		Providers: []LLMBridge{
			{Key: "cursor", Driver: "cursor-bridge", Model: "default"},
			{Key: "openai", Driver: "provider-bridge", Model: "gpt-4o-mini"},
		},
	}
	got, err := root.ActiveProvider()
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "openai" || got.Driver != "provider-bridge" || got.Model != "gpt-4o-mini" {
		t.Errorf("active provider mismatch: %+v", got)
	}
}

func TestLLMBridgeRootActiveProviderMissing(t *testing.T) {
	root := LLMBridgeRoot{
		ProviderKey: "nope",
		Providers:   []LLMBridge{{Key: "openai", Driver: "provider-bridge"}},
	}
	_, err := root.ActiveProvider()
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestOrchestratorConfigDefaultsAndOverrides(t *testing.T) {
	defaults := (OrchestratorConfig{}).WithDefaults()
	if defaults.Continuation.MaxInferenceTurns != 128 || defaults.Continuation.MaxToolCalls != 512 {
		t.Fatalf("unexpected continuation defaults: %+v", defaults.Continuation)
	}
	if defaults.Compaction.BackgroundThreshold != 0.70 || defaults.Compaction.BlockingThreshold != 0.88 {
		t.Fatalf("unexpected compaction defaults: %+v", defaults.Compaction)
	}

	custom := OrchestratorConfig{
		Continuation: ContinuationConfig{MaxInferenceTurns: 256},
		Compaction:   CompactionConfig{BackgroundThreshold: 0.60, BlockingThreshold: 0.90, PreserveRecentFraction: 0.40},
	}.WithDefaults()
	if custom.Continuation.MaxInferenceTurns != 256 {
		t.Fatalf("custom max inference turns lost: %+v", custom.Continuation)
	}
	if custom.Compaction.BackgroundThreshold != 0.60 || custom.Compaction.PreserveRecentFraction != 0.40 {
		t.Fatalf("custom compaction config lost: %+v", custom.Compaction)
	}
}
