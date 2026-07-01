package orchestrator

import (
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/prompts"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestBuildAutopilotContinuation_defaultStop(t *testing.T) {
	body := buildAutopilotContinuation(5, 1, nil, autopilotSignals{}, 0)
	if !strings.Contains(body, "sapaloq_stop") {
		t.Fatalf("default nudge should mention sapaloq_stop, got %q", body)
	}
	if !strings.Contains(body, prompts.GetInternal(prompts.KeyAutopilotDefaultStop)) {
		t.Fatalf("default nudge should use internal default-stop prompt, got %q", body)
	}
}

func TestBuildAutopilotContinuation_runningTasksEscalates(t *testing.T) {
	body := buildAutopilotContinuation(12, 6, nil, autopilotSignals{runningTasks: 1}, 0)
	if !strings.Contains(body, "sapaloq_stop") {
		t.Fatalf("escalated running-task nudge should mention stop, got %q", body)
	}
	if !strings.Contains(body, prompts.GetInternal(prompts.KeyAutopilotRunningEscalated)) {
		t.Fatalf("escalated running-task nudge should use running-escalated prompt, got %q", body)
	}
}

func TestBuildAutopilotContinuation_runningTasksEarly(t *testing.T) {
	body := buildAutopilotContinuation(0, 1, nil, autopilotSignals{runningTasks: 1}, 0)
	if !strings.Contains(body, prompts.GetInternal(prompts.KeyAutopilotRunning)) {
		t.Fatalf("early running-task nudge should use running prompt, got %q", body)
	}
}

func TestBuildAutopilotContinuation_clarificationPending(t *testing.T) {
	body := buildAutopilotContinuation(0, 4, nil, autopilotSignals{awaitingClarification: true}, 0)
	if !strings.Contains(body, prompts.GetInternal(prompts.KeyAutopilotClarificationPending)) {
		t.Fatalf("clarification-pending nudge expected, got %q", body)
	}
}

func TestActorTurnsToMessagesSkipsAutopilotEcho(t *testing.T) {
	turns := []chatstore.Turn{
		{Role: "user", Content: "build widgets"},
		{Role: "assistant", Content: "SapaLOQ received: <sapaloq:autopilot>\nContinue\n</sapaloq:autopilot>"},
		{Role: "assistant", Content: "Creating hero_widget template."},
	}
	msgs := actorTurnsToMessages(turns)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (echo skipped)", len(msgs))
	}
	if msgs[1].Content != "Creating hero_widget template." {
		t.Fatalf("unexpected assistant replay: %q", msgs[1].Content)
	}
}
