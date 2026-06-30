package config

import "strings"

// CredentialEnvKeys returns every process-environment variable name referenced
// by cfg that may need importing under systemd (llmBridge.providers[].credentialsEnv,
// webSearch.github.tokenEnv, …). Empty names are omitted; duplicates collapse.
// Order follows providers slice order, then web search.
func CredentialEnvKeys(cfg Config) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, p := range cfg.LLMBridge.Providers {
		add(p.CredentialsEnv)
	}
	add(cfg.WebSearch.WithDefaults().GitHub.TokenEnv)
	return out
}
