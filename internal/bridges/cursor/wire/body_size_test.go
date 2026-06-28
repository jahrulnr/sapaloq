package wire

import "testing"

func TestBuildChatBodySize(t *testing.T) {
	body := BuildChatBody([]ChatMessage{{Role: "user", Content: "Reply with exactly: pong"}}, "default")
	t.Logf("body_bytes=%d", len(body))
}
