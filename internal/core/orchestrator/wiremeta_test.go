package orchestrator

import (
	"encoding/json"
	"testing"

	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestActorTurnsToMessagesWireMetaReplay(t *testing.T) {
	meta := json.RawMessage(`{"driver":"gemini-bridge","model_parts":[{"functionCall":{"name":"echo","id":"1"},"thoughtSignature":"sig"}]}`)
	turns := []chatstore.Turn{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "", WireMeta: meta},
		{Role: "tool", Content: "result"},
	}
	msgs := actorTurnsToMessages(turns)
	if len(msgs) != 3 {
		t.Fatalf("msgs = %d, want 3", len(msgs))
	}
	if msgs[1].Role != "assistant" || len(msgs[1].WireMeta) == 0 {
		t.Fatalf("assistant wire meta lost: %+v", msgs[1])
	}
}
