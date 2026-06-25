package privacyfilter

import "testing"

func TestRedactSecrets(t *testing.T) {
	f := New()
	cases := []struct {
		name    string
		in      string
		wantHit bool
	}{
		{"openai", "here is the key sk-proj-abcdefghijklmnopqrstuvwxyz0123 ok", true},
		{"aws", "AKIAIOSFODNN7EXAMPLE", true},
		{"github", "token ghp_abcdefghijklmnopqrstuvwxyz0123456789 yes", true},
		{"private-key", "-----BEGIN OPENSSH PRIVATE KEY-----", true},
		{"context-password", "password: Sup3rSecretValue123", true},
		{"plain-prose", "the bow should be a small viewmodel held in first-person", false},
		{"email-passes", "contact me at alice@example.com", false},
		{"ip-passes", "the server is at 192.168.1.50", false},
		{"uuid-passes", "id 550e8400-e29b-41d4-a716-446655440000", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := f.Redact(c.in)
			if res.Hit != c.wantHit {
				t.Fatalf("Redact(%q): hit=%v want %v (redacted=%q)", c.in, res.Hit, c.wantHit, res.Redacted)
			}
			if c.wantHit && contains(res.Redacted, "[SECRET]") == false {
				t.Fatalf("Redact(%q): expected [SECRET] placeholder, got %q", c.in, res.Redacted)
			}
		})
	}
}

func TestRedactKeepsNonSecretContent(t *testing.T) {
	f := New()
	in := "before sk-proj-abcdefghijklmnopqrstuvwxyz0123 after"
	res := f.Redact(in)
	if !res.Hit {
		t.Fatal("expected a hit")
	}
	if !contains(res.Redacted, "before ") || !contains(res.Redacted, " after") {
		t.Fatalf("non-secret context should survive, got %q", res.Redacted)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
