package orchestrator

import (
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/privacyfilter"
)

// TestRedactToolResultsScrubsSecrets proves the exfiltration tail of a prompt
// injection is neutralised: even if the model is tricked into reading a secret,
// the secret value is replaced with [SECRET] before it enters the model context.
func TestRedactToolResultsScrubsSecrets(t *testing.T) {
	o := &Orchestrator{redactor: privacyfilter.New()}
	in := []string{
		"file contents:\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXkAAAA\nsk-proj-abcdefghijklmnopqrstuvwxyz0123\n",
	}
	out := o.redactToolResults(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	if strings.Contains(out[0], "sk-proj-abcdefghijklmnopqrstuvwxyz0123") {
		t.Fatalf("openai key leaked: %q", out[0])
	}
	if strings.Contains(out[0], "BEGIN OPENSSH PRIVATE KEY") {
		t.Fatalf("private key header leaked: %q", out[0])
	}
	if !strings.Contains(out[0], "[SECRET]") {
		t.Fatalf("expected [SECRET] placeholder, got %q", out[0])
	}
	// Non-secret context must survive so the model can still reason about the result.
	if !strings.Contains(out[0], "file contents:") {
		t.Fatalf("non-secret content should survive, got %q", out[0])
	}
}

// TestRedactToolResultsKeepsBenign proves ordinary tool output (no secrets) is
// passed through untouched - email/IP are intentionally NOT redacted.
func TestRedactToolResultsKeepsBenign(t *testing.T) {
	o := &Orchestrator{redactor: privacyfilter.New()}
	in := []string{"deployed to 192.168.1.50, contact admin@example.com when done"}
	out := o.redactToolResults(in)
	if out[0] != in[0] {
		t.Fatalf("benign content (email/IP) must pass through unchanged:\n got  %q\n want %q", out[0], in[0])
	}
}

// TestRedactToolResultsNilRedactor proves a struct built without a redactor
// (e.g. in unrelated tests) passes results through rather than panicking.
func TestRedactToolResultsNilRedactor(t *testing.T) {
	o := &Orchestrator{}
	in := []string{"sk-proj-abcdefghijklmnopqrstuvwxyz0123"}
	out := o.redactToolResults(in)
	if len(out) != 1 || out[0] != in[0] {
		t.Fatalf("nil redactor should pass through unchanged, got %v", out)
	}
}

// TestRedactedResultStillWrappedAsUntrusted proves the two defences compose:
// a redacted result still gets wrapped in <untrusted_data> by toolObservationBody.
func TestRedactedResultStillWrappedAsUntrusted(t *testing.T) {
	o := &Orchestrator{redactor: privacyfilter.New()}
	redacted := o.redactToolResults([]string{"key sk-proj-abcdefghijklmnopqrstuvwxyz0123 here"})
	body := toolObservationBody(redacted)
	if !strings.Contains(body, "[SECRET]") {
		t.Fatalf("secret should be redacted inside the observation body: %q", body)
	}
	if !strings.Contains(body, untrustedClose) {
		t.Fatalf("redacted result should still be wrapped as untrusted data: %q", body)
	}
}
