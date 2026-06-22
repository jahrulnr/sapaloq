package provider

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
)

// TestExplainStreamErrorWrapsDeadline verifies the provider bridge (tokenrouter
// / OpenAI / Claude / Kimi) turns an opaque "context deadline exceeded" into an
// actionable message naming the timeout and the config knob.
func TestExplainStreamErrorWrapsDeadline(t *testing.T) {
	b := &Bridge{entry: config.LLMBridge{RequestTimeoutSec: 600}}

	got := b.explainStreamError(errors.New("context deadline exceeded"))
	for _, want := range []string{"timed out", "600", "requestTimeoutSec"} {
		if !strings.Contains(got, want) {
			t.Fatalf("explained = %q, want it to contain %q", got, want)
		}
	}

	other := "401 unauthorized"
	if got := b.explainStreamError(errors.New(other)); got != other {
		t.Fatalf("non-deadline error rewritten: %q", got)
	}
	if got := b.explainStreamError(nil); got != "" {
		t.Fatalf("nil error = %q, want empty", got)
	}
}

// TestWireOptionsUseConfiguredTimeout confirms the per-request timeout from the
// provider entry flows into the WireOptions the bridge builds (so a long
// sub-agent step isn't truncated at the old 120s default).
func TestWireOptionsUseConfiguredTimeout(t *testing.T) {
	// Unset entry → default 600s.
	if got := (config.LLMBridge{}).RequestTimeout(); got != time.Duration(config.DefaultRequestTimeoutSec)*time.Second {
		t.Fatalf("default = %v, want %ds", got, config.DefaultRequestTimeoutSec)
	}
	// Explicit honored.
	if got := (config.LLMBridge{RequestTimeoutSec: 900}).RequestTimeout(); got != 900*time.Second {
		t.Fatalf("override = %v, want 900s", got)
	}
}
