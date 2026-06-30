package config

import "testing"

func TestCredentialEnvKeysFromProviders(t *testing.T) {
	cfg := Config{
		LLMBridge: LLMBridgeRoot{
			ProviderKey: "a",
			Providers: []LLMBridge{
				{Key: "a", CredentialsEnv: "TOKEN_A"},
				{Key: "b", CredentialsEnv: "TOKEN_B"},
				{Key: "c", CredentialsEnv: ""},
				{Key: "d", CredentialsEnv: "TOKEN_A"},
			},
		},
		WebSearch: WebSearchConfig{
			GitHub: WebSearchGitHub{TokenEnv: "MY_GITHUB"},
		},
	}
	got := CredentialEnvKeys(cfg)
	want := []string{"TOKEN_A", "TOKEN_B", "MY_GITHUB"}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys[%d] = %q, want %q (full %v)", i, got[i], want[i], got)
		}
	}
}

func TestCredentialEnvKeysWebSearchDefault(t *testing.T) {
	cfg := Config{
		LLMBridge: LLMBridgeRoot{
			Providers: []LLMBridge{{Key: "x", CredentialsEnv: "PROVIDER_TOKEN"}},
		},
	}
	got := CredentialEnvKeys(cfg)
	if len(got) != 2 || got[0] != "PROVIDER_TOKEN" || got[1] != "GITHUB_TOKEN" {
		t.Fatalf("keys = %v", got)
	}
}
