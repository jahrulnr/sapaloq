package orchestrator

import (
	"strings"
	"testing"
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
