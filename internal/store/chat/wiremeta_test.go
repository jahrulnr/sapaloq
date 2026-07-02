package chat_test

import (
	"context"
	"encoding/json"
	"testing"

	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestAppendTurnWithWireMetaAllowsEmptyContent(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "gemini", "flash")
	if err != nil {
		t.Fatal(err)
	}
	meta := json.RawMessage(`{"driver":"gemini-bridge","model_parts":[{"functionCall":{"name":"echo"}}]}`)
	id, err := store.AppendTurnIDWithWireMeta(ctx, sessionID, "assistant", "", 0, "1", meta)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected turn id")
	}
	turns, err := store.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || len(turns[0].WireMeta) == 0 {
		t.Fatalf("turns = %+v", turns)
	}
}
