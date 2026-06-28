package orchestrator

import (
	"context"
	"testing"

	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestSessionTranscriptSegmentLatestTail(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	id1, _ := store.AppendTurnID(ctx, sessionID, "user", "old", 1)
	id2, _ := store.AppendTurnID(ctx, sessionID, "assistant", "old reply", 1)
	id3, _ := store.AppendTurnID(ctx, sessionID, "user", "tail user", 1)
	id4, _ := store.AppendTurnID(ctx, sessionID, "assistant", "tail reply", 1)
	_, err = store.CreateCheckpoint(ctx, sessionID, "summary one", "test",
		chatstore.TailPolicy{ArchiveTurnIDs: []int64{id1, id2}, TailStartTurnID: id3}, func(s string) int { return len(s) })
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{chat: store}
	_, meta, err := o.SessionTranscriptSegment(ctx, sessionID, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.IsLatest {
		t.Fatalf("IsLatest = false, want true")
	}
	if !meta.HasOlder {
		t.Fatal("expected older segment hint")
	}
	turns, _ := store.ActiveTurns(ctx, sessionID, true)
	latestSlice, _ := sliceTurnsForSegment(turns, mustCheckpoints(t, store, ctx, sessionID), -1)
	if len(latestSlice) < 3 {
		t.Fatalf("latest segment len = %d, want checkpoint + tail (%d,%d)", len(latestSlice), id3, id4)
	}
	_ = id4
}

func mustCheckpoints(t *testing.T, store *chatstore.Store, ctx context.Context, sessionID string) []chatstore.Checkpoint {
	t.Helper()
	ck, err := store.Checkpoints(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return ck
}
