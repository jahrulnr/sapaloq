package orchestrator

import (
	"strings"
	"testing"
	"time"
)

// TestClarificationResumeReconstruction verifies that a task paused on a
// clarification can be answered: buildSubAgentMessages replays the persisted
// transcript and appends the answer nudge.
func TestClarificationResumeReconstruction(t *testing.T) {
	o := &Orchestrator{memoryDir: t.TempDir()}
	rec := &taskRecord{
		ID:     "task-clar-1",
		Role:   "task-runner",
		Status: "awaiting_clarification",
		Task:   "implement the feature",
		Transcript: []taskTurn{
			{Role: "assistant", Content: "I inspected the repo."},
			{Role: "user", Content: "[Tool results]\nfound main.go"},
		},
		Answer: "use the v2 API",
	}

	msgs := o.buildSubAgentMessages(rec)
	joined := ""
	for _, m := range msgs {
		joined += m.Role + ":" + m.Content + "\n"
	}
	if !strings.Contains(joined, "I inspected the repo.") {
		t.Fatalf("transcript not replayed into messages:\n%s", joined)
	}
	if !strings.Contains(joined, "found main.go") {
		t.Fatalf("tool results turn not replayed:\n%s", joined)
	}
	if !strings.Contains(joined, "use the v2 API") {
		t.Fatalf("answer nudge missing:\n%s", joined)
	}
	if !strings.Contains(joined, "implement the feature") {
		t.Fatalf("original task missing:\n%s", joined)
	}
}

// TestAnswerClarificationFlipsStatus verifies the persisted-state transition the
// answer handler performs (without spawning a real loop): paused → in_progress,
// answer set, question cleared, transcript preserved.
func TestAnswerClarificationFlipsStatus(t *testing.T) {
	o := &Orchestrator{memoryDir: t.TempDir()}
	rec := taskRecord{
		ID:         "task-clar-2",
		SessionID:  "s1",
		Role:       "task-runner",
		Status:     "awaiting_clarification",
		Question:   "Which API version?",
		Transcript: []taskTurn{{Role: "assistant", Content: "prior context"}},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := o.writeTask(rec); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// latestAwaitingTaskID should find it.
	if got := o.latestAwaitingTaskID("s1"); got != "task-clar-2" {
		t.Fatalf("latestAwaitingTaskID = %q, want task-clar-2", got)
	}

	// Apply the same mutation the handler does.
	loaded, err := o.readTask("task-clar-2")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if loaded.Status != "awaiting_clarification" {
		t.Fatalf("precondition: status=%s", loaded.Status)
	}
	loaded.Answer = "v2"
	loaded.Question = ""
	loaded.Status = "in_progress"
	if err := o.writeTask(loaded); err != nil {
		t.Fatalf("write: %v", err)
	}

	after, err := o.readTask("task-clar-2")
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if after.Status != "in_progress" {
		t.Fatalf("status not flipped: %s", after.Status)
	}
	if after.Answer != "v2" {
		t.Fatalf("answer not set: %q", after.Answer)
	}
	if after.Question != "" {
		t.Fatalf("question not cleared: %q", after.Question)
	}
	if len(after.Transcript) != 1 || after.Transcript[0].Content != "prior context" {
		t.Fatalf("transcript not preserved: %+v", after.Transcript)
	}

	// No awaiting task remains.
	if got := o.latestAwaitingTaskID("s1"); got != "" {
		t.Fatalf("expected no awaiting task after answer, got %q", got)
	}
}

// TestAppendTranscriptCaps verifies per-turn content capping and empty skip.
func TestAppendTranscriptCaps(t *testing.T) {
	rec := &taskRecord{}
	rec.appendTranscript("user", "   ") // empty after trim → skipped
	if len(rec.Transcript) != 0 {
		t.Fatalf("empty content should be skipped")
	}
	big := strings.Repeat("x", maxTranscriptTurnBytes+500)
	rec.appendTranscript("assistant", big)
	if len(rec.Transcript) != 1 {
		t.Fatalf("expected 1 turn")
	}
	if len(rec.Transcript[0].Content) > maxTranscriptTurnBytes+len("…[truncated]") {
		t.Fatalf("content not capped: %d", len(rec.Transcript[0].Content))
	}
	if !strings.HasSuffix(rec.Transcript[0].Content, "[truncated]") {
		t.Fatalf("expected truncation marker")
	}
}
