package wire

import "testing"

// TestAgentHostRespectsGhostMode confirms that the privacy-vs-nonprivacy
// Agent host switch returns the expected hostname for each ghostMode value.
// Mirrors 9router/src/lib/oauth/constants/oauth.js.
func TestAgentHostRespectsGhostMode(t *testing.T) {
	if got := AgentHost(true); got != AgentPrivacyHost {
		t.Fatalf("AgentHost(true) = %q, want %q", got, AgentPrivacyHost)
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
