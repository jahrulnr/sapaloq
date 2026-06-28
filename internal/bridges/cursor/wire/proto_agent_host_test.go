package wire

import "testing"

// TestAgentHostMatchesCursorAgent confirms we always use the non-privacy host
// like 9router/open-sse/executors/cursorAgent.js (ghost mode is header-only).
func TestAgentHostMatchesCursorAgent(t *testing.T) {
	if got := AgentHost(true); got != AgentNonPrivacyHost {
		t.Fatalf("AgentHost(true) = %q, want %q", got, AgentNonPrivacyHost)
	}
	if got := AgentHost(false); got != AgentNonPrivacyHost {
		t.Fatalf("AgentHost(false) = %q, want %q", got, AgentNonPrivacyHost)
	}
}

// TestAgentHostsDiffer sanity-checks that the privacy and non-privacy hosts
// are distinct - otherwise the switch is a no-op.
func TestAgentHostsDiffer(t *testing.T) {
	if AgentPrivacyHost == AgentNonPrivacyHost {
		t.Fatalf("privacy and non-privacy hosts must differ, both = %q", AgentPrivacyHost)
	}
	if AgentPrivacyHost == "" || AgentNonPrivacyHost == "" {
		t.Fatalf("agent hosts must not be empty (privacy=%q non-privacy=%q)",
			AgentPrivacyHost, AgentNonPrivacyHost)
	}
}

// TestAgentAgentHostAlias verifies the legacy constant name still resolves
// to the non-privacy host (keeps external callers compiling).
func TestAgentAgentHostAlias(t *testing.T) {
	if AgentAgentHost != AgentNonPrivacyHost {
		t.Fatalf("AgentAgentHost = %q, want %q (alias)", AgentAgentHost, AgentNonPrivacyHost)
	}
}
