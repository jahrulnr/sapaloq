package wire

import "testing"

func TestAgentBodyLenMatchesReference(t *testing.T) {
	body := BuildAgentRequestBody(AgentRunOptions{
		UserText:       "reply pong",
		ModelID:        "default",
		ConversationID: "node-direct-test",
		MessageID:      "fixed-msg-id",
	})
	// Reference length from 9router cursorAgentProtobuf.js with the same IDs.
	const wantLen = 97
	if len(body) != wantLen {
		t.Fatalf("agent body len=%d want=%d payload=%x", len(body), wantLen, body)
	}
}
