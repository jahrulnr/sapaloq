package debug

import "testing"

func TestConfigure(t *testing.T) {
	Configure(false, false)
	if Enabled() || Verbose() {
		t.Fatal("expected off")
	}
	Configure(true, false)
	if !Enabled() || Verbose() {
		t.Fatal("expected debug only")
	}
	Configure(false, true)
	if !Enabled() || !Verbose() {
		t.Fatal("expected verbose")
	}
}

func TestRedactSecret(t *testing.T) {
	if got := RedactSecret("abcd1234wxyz"); got != "abcd…wxyz" {
		t.Fatalf("got %q", got)
	}
}
