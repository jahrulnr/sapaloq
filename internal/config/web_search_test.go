package config

import (
	"testing"
	"time"
)

func TestWebSearchConfigWithDefaults(t *testing.T) {
	got := (WebSearchConfig{}).WithDefaults()
	if got.Limit != 8 {
		t.Fatalf("Limit = %d, want 8", got.Limit)
	}
	if got.TimeoutSec != 20 {
		t.Fatalf("TimeoutSec = %d, want 20", got.TimeoutSec)
	}
	if got.GitHub.TokenEnv != "GITHUB_TOKEN" {
		t.Fatalf("GitHub.TokenEnv = %q, want GITHUB_TOKEN", got.GitHub.TokenEnv)
	}
}

func TestWebSearchConfigWithDefaultsPreservesOverrides(t *testing.T) {
	got := (WebSearchConfig{
		Limit:      3,
		TimeoutSec: 7,
		GitHub:     WebSearchGitHub{TokenEnv: "SAPALOQ_GITHUB_TOKEN"},
	}).WithDefaults()
	if got.Limit != 3 || got.TimeoutSec != 7 || got.GitHub.TokenEnv != "SAPALOQ_GITHUB_TOKEN" {
		t.Fatalf("overrides were not preserved: %+v", got)
	}
}

func TestWebSearchConfigSearchwireConfig(t *testing.T) {
	got := (WebSearchConfig{
		Limit:      5,
		TimeoutSec: 9,
		GitHub: WebSearchGitHub{
			Token:    "  token-value  ",
			TokenEnv: "GH_TOKEN_CUSTOM",
		},
	}).SearchwireConfig()

	if got.Limit != 5 || got.Timeout != 9*time.Second {
		t.Fatalf("searchwire limits = limit %d timeout %s", got.Limit, got.Timeout)
	}
	if got.UserAgent != "SapaLOQ/1.0 (+https://github.com/jahrulnr/sapaloq)" {
		t.Fatalf("UserAgent = %q", got.UserAgent)
	}
	if got.GitHub.Token != "token-value" || got.GitHub.TokenEnv != "GH_TOKEN_CUSTOM" {
		t.Fatalf("GitHub mapping = %+v", got.GitHub)
	}
}
