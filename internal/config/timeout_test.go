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
