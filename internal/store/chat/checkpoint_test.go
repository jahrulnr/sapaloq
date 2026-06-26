package chat

import (
	"context"
	"testing"
)

func estimateFixed(s string) int { return len(s) }

func TestCreateCheckpointArchivesAndAdvancesIndex(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, err := store.Reset(ctx, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	id1, _ := store.AppendTurnID(ctx, sessionID, "user", "hello", 1)
	id2, _ := store.AppendTurnID(ctx, sessionID, "assistant", "hi there", 1)
	id3, _ := store.AppendTurnID(ctx, sessionID, "user", "do thing", 1)
	id4, _ := store.AppendTurnID(ctx, sessionID, "assistant", "done", 1)

	// Archive first two turns; tail starts at id3 (preceding user of last assistant).
	res, err := store.CreateCheckpoint(ctx, sessionID, "## goals\n- thing done", "model",
		TailPolicy{ArchiveTurnIDs: []int64{id1, id2}, TailStartTurnID: id3}, estimateFixed)
	if err != nil {
		t.Fatal(err)
	}
	if res.Index != 1 {
		t.Fatalf("index = %d, want 1", res.Index)
	}
	if res.CompactedTurns != 2 {
		t.Fatalf("compactedTurns = %d, want 2", res.CompactedTurns)
	}
	if res.TailStartTurnID != id3 {
		t.Fatalf("tailStartTurnID = %d, want %d", res.TailStartTurnID, id3)
	}

	// Active (in-context) turns should now be: tail (id3, id4) + checkpoint marker.
	active, err := store.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 3 {
		t.Fatalf("active turns = %d, want 3 (tail 2 + checkpoint)", len(active))
	}
	if active[0].ID != id3 || active[1].ID != id4 {
		t.Fatalf("active tail = [%d %d], want [%d %d]", active[0].ID, active[1].ID, id3, id4)
	}
	if active[2].Role != "checkpoint" || active[2].CheckpointIndex != 1 {
		t.Fatalf("checkpoint turn = %+v", active[2])
	}
	if active[2].IncludedInContext != true {
		t.Fatalf("checkpoint marker must be included_in_context=1")
	}

	// All turns (UI view) keeps archived rows.
	all, err := store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 { // 4 original + checkpoint marker
		t.Fatalf("ui turns = %d, want 5", len(all))
	}

	// Latest checkpoint reflects the row we wrote.
	latest, err := store.LatestCheckpoint(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Index != 1 || latest.Reason != "model" {
		t.Fatalf("latest checkpoint = %+v", latest)
	}

	// Second checkpoint advances the monotonic index.
	res2, err := store.CreateCheckpoint(ctx, sessionID, "## goals\n- more", "force_headroom",
		TailPolicy{ArchiveTurnIDs: []int64{id3, id4}}, estimateFixed)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Index != 2 {
		t.Fatalf("second index = %d, want 2", res2.Index)
	}
	list, err := store.Checkpoints(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[1].Index != 2 {
		t.Fatalf("checkpoints = %+v", list)
	}
}

func TestLatestCheckpointEmpty(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID, _ := store.Reset(ctx, "test", "test")
	_, err = store.LatestCheckpoint(ctx, sessionID)
	if err == nil {
		t.Fatal("expected error for no checkpoint, got nil")
	}
}
