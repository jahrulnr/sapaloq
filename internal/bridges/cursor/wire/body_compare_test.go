package wire

import (
	"os"
	"testing"
)

func TestWriteGoBodyForCompare(t *testing.T) {
	if os.Getenv("WRITE_GO_BODY") == "" {
		t.Skip("set WRITE_GO_BODY=1")
	}
	body := BuildChatBody([]ChatMessage{{Role: "user", Content: "Reply with exactly: pong"}}, "default")
	if err := os.WriteFile("/tmp/go-body.bin", body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bytes", len(body))
}
