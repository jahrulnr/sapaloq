package config

import (
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
