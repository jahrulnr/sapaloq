package config

import (
	"testing"
	"time"
)

func TestRequestTimeoutDefaultAndOverride(t *testing.T) {
	// Unset → default.
	if got := (LLMBridge{}).RequestTimeout(); got != time.Duration(DefaultRequestTimeoutSec)*time.Second {
		t.Fatalf("default timeout = %v, want %ds", got, DefaultRequestTimeoutSec)
	}
	// Negative/zero → default (guards a malformed config).
	if got := (LLMBridge{RequestTimeoutSec: -5}).RequestTimeout(); got != time.Duration(DefaultRequestTimeoutSec)*time.Second {
		t.Fatalf("negative timeout = %v, want default", got)
	}
	// Explicit value honored.
	if got := (LLMBridge{RequestTimeoutSec: 900}).RequestTimeout(); got != 900*time.Second {
		t.Fatalf("override timeout = %v, want 900s", got)
	}
}

// TestDefaultRequestTimeoutExceedsOldHardcoded guards against regressing to the
// old 120s wire default that truncated long sub-agent generations.
func TestDefaultRequestTimeoutExceedsOldHardcoded(t *testing.T) {
	if DefaultRequestTimeoutSec <= 120 {
		t.Fatalf("DefaultRequestTimeoutSec=%d must exceed the old 120s wire default", DefaultRequestTimeoutSec)
	}
}

// TestResolveMaxRetries covers default, disable, explicit, and clamp behaviour
// of the provider-bridge pre-stream retry knob.
func TestResolveMaxRetries(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"unset defaults", 0, DefaultMaxRetries},
		{"explicit value", 3, 3},
		{"negative disables", -1, 0},
		{"large negative disables", -99, 0},
		{"clamped to cap", 50, MaxRetriesCap},
		{"exactly cap", MaxRetriesCap, MaxRetriesCap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (LLMBridge{MaxRetries: tc.in}).ResolveMaxRetries(); got != tc.want {
				t.Fatalf("ResolveMaxRetries(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestDefaultMaxRetriesMatchesCLIResilience documents the intent: the default
// should provide meaningful resilience against flaky-gateway 500s (the official
// Blackbox CLI uses the OpenAI SDK default of 3; we go a little higher).
func TestDefaultMaxRetriesMatchesCLIResilience(t *testing.T) {
	if DefaultMaxRetries < 3 {
		t.Fatalf("DefaultMaxRetries=%d should be >= 3 to absorb transient gateway 500s", DefaultMaxRetries)
	}
	if DefaultMaxRetries > MaxRetriesCap {
		t.Fatalf("DefaultMaxRetries=%d must not exceed MaxRetriesCap=%d", DefaultMaxRetries, MaxRetriesCap)
	}
}

// TestStreamEnabled covers the tri-state stream toggle: an absent field keeps
// the historical streaming default (backward-compatible with every existing
// config), while an explicit true/false is honoured verbatim.
func TestStreamEnabled(t *testing.T) {
	tr, fa := true, false
	cases := []struct {
		name string
		in   *bool
		want bool
	}{
		{"nil defaults to streaming", nil, true},
		{"explicit true", &tr, true},
		{"explicit false", &fa, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (LLMBridge{Stream: tc.in}).StreamEnabled(); got != tc.want {
				t.Fatalf("StreamEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}
