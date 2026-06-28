package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestPurgeSessionProgressForRetryDropsStaleTools(t *testing.T) {
	root := t.TempDir()
	progressDir := filepath.Join(root, "progress")
	store, err := chatstore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sessionID, err := store.ActiveSession(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{
		cfg:       config.DefaultConfig(),
		chat:      store,
		entry:     config.LLMBridge{Key: "p", Model: "m"},
		memoryDir: root,
		progress:  newAsyncProgressWriter(ProgressWriter{Dir: progressDir}),
	}
	userID, err := store.AppendTurnIDWithGeneration(ctx, sessionID, "user", "map themes", 3, "1")
	if err != nil {
		t.Fatal(err)
	}
	gen := "99"
	now := time.Now().UTC()
	toolEv := bridge.NewEvent(bridge.EventToolUpdate)
	toolEv.SessionID = sessionID
	toolEv.GenerationID = gen
	toolEv.ToolCall = &parse.ToolCall{ID: "t1", Name: "exec", Arguments: []byte(`{"command":"ls"}`)}
	toolEv.ToolResult = "ok"
	toolEv.Status = "completed"
	toolEv.At = now.Add(time.Second)
	if err := o.progress.inner.Append(sessionID, toolEv); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTurnIDWithGeneration(ctx, sessionID, "assistant", "failed answer", 4, gen); err != nil {
		t.Fatal(err)
	}

	before, err := o.SessionTranscript(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if countKind(before, bridge.TranscriptTool) == 0 {
		t.Fatalf("expected tool rows before purge: %#v", before)
	}

	userTurn, err := store.Turn(ctx, sessionID, userID)
	if err != nil {
		t.Fatal(err)
	}
	allTurns, err := store.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	dropGens := generationIDsAfterTurn(allTurns, userTurn)
	if err := store.DeleteAfterTurn(ctx, sessionID, userID); err != nil {
		t.Fatal(err)
	}
	if err := o.purgeSessionProgressForRetry(sessionID, dropGens, userTurn.CreatedAt); err != nil {
		t.Fatal(err)
	}

	after, err := o.SessionTranscript(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if countKind(after, bridge.TranscriptTool) != 0 {
		t.Fatalf("tool rows must be purged on retry, got %#v", after)
	}
	if len(after) != 1 || after[0].Kind != bridge.TranscriptUser {
		t.Fatalf("expected only user turn after purge, got %#v", after)
	}
	raw, err := os.ReadFile(filepath.Join(progressDir, "orch-"+sessionID+".jsonl"))
	if err == nil && len(raw) > 0 {
		t.Fatalf("progress file should be empty or removed after purge, got %q", raw)
	}
}

func countKind(entries []bridge.TranscriptEntry, kind bridge.TranscriptEntryKind) int {
	n := 0
	for _, e := range entries {
		if e.Kind == kind {
			n++
		}
	}
	return n
}
