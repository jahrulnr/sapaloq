package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func tr(id int64, role string) chatstore.Turn { return chatstore.Turn{ID: id, Role: role} }

func TestComputeTailPreserveAnchorsLastAssistant(t *testing.T) {
	turns := []chatstore.Turn{
		tr(1, "user"),
		tr(2, "assistant"),
		tr(3, "user"),
		tr(4, "assistant"), // last assistant -> anchor
	}
	plan := computeTailPreserve(turns, 4, true)
	if plan.tailStart != 2 {
		t.Fatalf("tailStart = %d, want 2 (last assistant + preceding user)", plan.tailStart)
	}
	if len(plan.archiveTurnIDs) != 2 {
		t.Fatalf("archiveTurnIDs = %v, want [1 2]", plan.archiveTurnIDs)
	}
	// Tail must include the anchored assistant turn (id 4) and the preceding user (id 3).
	if plan.tailStartTurnID != 3 {
		t.Fatalf("tailStartTurnID = %d, want 3", plan.tailStartTurnID)
	}
}

func TestComputeTailPreserveNoPrecedingUser(t *testing.T) {
	turns := []chatstore.Turn{
		tr(1, "user"),
		tr(2, "assistant"),
		tr(3, "tool"),
		tr(4, "assistant"),
	}
	plan := computeTailPreserve(turns, 4, false)
	// Without preceding-user pairing, anchor == lastAssistant index 3.
	// desiredStart = 3 - 4 + 1 = 0; but anchor 3 wins => tailStart = 3.
	if plan.tailStart != 3 {
		t.Fatalf("tailStart = %d, want 3", plan.tailStart)
	}
	if plan.tailStartTurnID != 4 {
		t.Fatalf("tailStartTurnID = %d, want 4", plan.tailStartTurnID)
	}
}

func TestComputeTailPreserveKeepRecentCaps(t *testing.T) {
	turns := []chatstore.Turn{
		tr(1, "user"),
		tr(2, "assistant"),
		tr(3, "user"),
		tr(4, "assistant"),
		tr(5, "user"),
		tr(6, "assistant"),
		tr(7, "user"),
		tr(8, "assistant"), // anchor at index 7
	}
	plan := computeTailPreserve(turns, 2, true)
	// desiredStart = 7 - 2 + 1 = 6; anchor with preceding user = 6. tailStart = 6.
	if plan.tailStart != 6 {
		t.Fatalf("tailStart = %d, want 6", plan.tailStart)
	}
	if plan.tailStartTurnID != 7 {
		t.Fatalf("tailStartTurnID = %d, want 7", plan.tailStartTurnID)
	}
	if len(plan.archiveTurnIDs) != 6 {
		t.Fatalf("archiveTurnIDs len = %d, want 6", len(plan.archiveTurnIDs))
	}
}

func TestComputeTailPreserveNoAssistantFallback(t *testing.T) {
	turns := []chatstore.Turn{
		tr(1, "user"),
		tr(2, "user"),
		tr(3, "tool"),
	}
	plan := computeTailPreserve(turns, 2, true)
	// No assistant anchor: keep soft cap of 2 -> start = 1.
	if plan.tailStart != 1 {
		t.Fatalf("tailStart = %d, want 1", plan.tailStart)
	}
}

func TestComputeTailPreserveNothingToArchive(t *testing.T) {
	turns := []chatstore.Turn{tr(1, "user"), tr(2, "assistant")}
	plan := computeTailPreserve(turns, 4, true)
	// desiredStart = 1 - 4 + 1 = -2 -> 0; anchor with preceding user = 0. tailStart = 0.
	if plan.tailStart != 0 {
		t.Fatalf("tailStart = %d, want 0 (nothing to archive)", plan.tailStart)
	}
	if len(plan.archiveTurnIDs) != 0 {
		t.Fatalf("archiveTurnIDs = %v, want empty", plan.archiveTurnIDs)
	}
}

func TestIsReadOnlyAssessmentTool(t *testing.T) {
	yes := map[string]bool{
		"read_file": true, "search": true, "list_dir": true, "glob": true,
		"read_image": true, "web_search": true, "web_fetch": true,
	}
	for name, want := range yes {
		if got := isReadOnlyAssessmentTool(name); got != want {
			t.Errorf("isReadOnlyAssessmentTool(%q) = %v, want %v", name, got, want)
		}
	}
	for _, name := range []string{"write_file", "run_command", "sapaloq_stop", "sapaloq_compact_session", "request_clarification", "delete_file"} {
		if isReadOnlyAssessmentTool(name) {
			t.Errorf("isReadOnlyAssessmentTool(%q) = true, want false (side-effecting/lifecycle)", name)
		}
	}
}

func TestOrchestratorFallbackCheckpointArchivesOldTurns(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := store.AppendTurn(ctx, sessionID, role, fmt.Sprintf("turn %d content", i), 50); err != nil {
			t.Fatal(err)
		}
	}
	o := &Orchestrator{chat: store, cfg: config.Config{Orchestrator: config.DefaultOrchestratorConfig()}}
	res, ok, err := o.orchestratorFallbackCheckpoint(ctx, sessionID, "test")
	if err != nil || !ok {
		t.Fatalf("fallback checkpoint: ok=%v err=%v", ok, err)
	}
	if res.Index != 1 {
		t.Fatalf("index = %d, want 1", res.Index)
	}
	active, err := store.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) > 6 {
		t.Fatalf("expected shrunk active tail, got %d turns", len(active))
	}
}

func TestEffectiveContextPercentUsesPersistedAttachmentWeight(t *testing.T) {
	ctx := context.Background()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.ActiveSession(ctx, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a pasted image turn: huge token_estimate in DB, placeholder in
	// the live slice after extractImages strips the inline data URI.
	huge := strings.Repeat("A", 3_600_000) // ~900k tokens at len/4
	if err := store.AppendTurn(ctx, sessionID, "user", "![img.png](data:image/png;base64,"+huge+")", estimateTextTokens("![img.png](data:image/png;base64,"+huge+")")); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{chat: store}
	live, images := extractImages([]bridge.Message{{Role: "user", Content: "[Image attachment: img.png]"}})
	if len(images) != 0 {
		t.Fatalf("expected stripped live slice without vision payload, got %d images", len(images))
	}
	const window = 900_000
	livePct := o.contextPercent(live, window)
	if livePct >= 50 {
		t.Fatalf("live slice alone should be tiny after image strip, got %d%%", livePct)
	}
	effective := o.effectiveContextPercent(ctx, sessionID, live, window)
	if effective < 95 {
		t.Fatalf("effective = %d%%, want >=95 from persisted attachment estimate", effective)
	}
	if !o.contextHeadroomReached(ctx, sessionID, live, window, 0.05) {
		t.Fatal("headroom should be reached when persisted usage exceeds 95%")
	}
}
