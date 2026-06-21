package cursor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
)

// TestExplainStreamErrorWrapsDeadline verifies an opaque "context deadline
// exceeded" is turned into an actionable message naming the timeout and the
// config knob, while unrelated errors pass through unchanged.
func TestExplainStreamErrorWrapsDeadline(t *testing.T) {
	b := &Bridge{timeout: 600 * time.Second}

	got := b.explainStreamError(errors.New("context deadline exceeded"))
	for _, want := range []string{"timed out", "600", "requestTimeoutSec"} {
		if !strings.Contains(got, want) {
			t.Fatalf("explained = %q, want it to contain %q", got, want)
		}
	}

	// Non-deadline errors are passed through verbatim.
	other := "some other transport failure"
	if got := b.explainStreamError(errors.New(other)); got != other {
		t.Fatalf("non-deadline error rewritten: %q", got)
	}

	if got := b.explainStreamError(nil); got != "" {
		t.Fatalf("nil error = %q, want empty", got)
	}
}

// TestNewBridgeResolvesTimeoutFromEntry confirms the configured per-request
// timeout flows from the provider entry into the bridge.
func TestNewBridgeResolvesTimeoutFromEntry(t *testing.T) {
	forceMockCredentials(t)
	entry, runtime := defaultTestEntry()
	entry.RequestTimeoutSec = 777
	b, err := New(entry, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if b.timeout != 777*time.Second {
		t.Fatalf("bridge timeout = %v, want 777s", b.timeout)
	}

	// Unset entry → default.
	entry2, runtime2 := defaultTestEntry()
	entry2.RequestTimeoutSec = 0
	b2, err := New(entry2, runtime2)
	if err != nil {
		t.Fatal(err)
	}
	if b2.timeout != time.Duration(config.DefaultRequestTimeoutSec)*time.Second {
		t.Fatalf("default bridge timeout = %v, want %ds", b2.timeout, config.DefaultRequestTimeoutSec)
	}
}
