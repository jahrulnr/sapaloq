package orchestrator

import (
	"strings"
	"testing"

	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func TestBuildAutopilotContinuation_agentSessionDefersStop(t *testing.T) {
	body := buildAutopilotContinuation(5, 1, nil, autopilotSignals{}, 0)
	if !strings.Contains(body, "verify") {
		t.Fatalf("agent session should encourage verification before stop, got %q", body)
	}
	if strings.Contains(body, "Invoke `sapaloq_stop` silently now") {
		t.Fatalf("early agent nudge must not hard-push stop, got %q", body)
	}
}

func TestBuildAutopilotContinuation_agentSessionEscalatesAfterStreak(t *testing.T) {
	body := buildAutopilotContinuation(12, 6, nil, autopilotSignals{}, 0)
	if !strings.Contains(body, "sapaloq_stop") {
		t.Fatalf("escalated agent nudge should mention stop, got %q", body)
	}
	if !strings.Contains(body, "tool action") {
		t.Fatalf("escalated agent nudge should still allow finishing work, got %q", body)
	}
}

func TestBuildAutopilotContinuation_chatOnlyEscalatesStop(t *testing.T) {
	body := buildAutopilotContinuation(0, 4, nil, autopilotSignals{}, 0)
	if !strings.Contains(body, "Invoke `sapaloq_stop` silently now") {
		t.Fatalf("chat-only escalated nudge should push silent stop, got %q", body)
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
