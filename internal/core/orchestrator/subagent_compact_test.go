package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func TestCompactMessagesWithSummary(t *testing.T) {
	messages := []bridge.Message{
		{Role: "system", Content: "persona"},
		{Role: "user", Content: "task"},
	}
	for i := 0; i < 12; i++ {
		messages = append(messages, bridge.Message{Role: "assistant", Content: "step " + string(rune('a'+i))})
	}
	compacted := compactMessagesWithSummary(messages, "build app", "## Goals\n- ship", 0.30)
	if len(compacted) >= len(messages) {
		t.Fatalf("expected shrink, got %d -> %d", len(messages), len(compacted))
	}
	if !strings.Contains(compacted[1].Content, "[Checkpoint summary]") {
		t.Fatalf("missing checkpoint header: %q", compacted[1].Content)
	}
	if !strings.Contains(compacted[1].Content, "## Goals") {
		t.Fatalf("missing model summary: %q", compacted[1].Content)
	}
}

func TestHandleSubAgentCompactSession(t *testing.T) {
	o := &Orchestrator{}
	msgs := []bridge.Message{
		{Role: "system", Content: "persona"},
		{Role: "user", Content: "do work"},
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs, bridge.Message{Role: "assistant", Content: "progress"})
	}
	ctx := &subAgentCompactCtx{messages: &msgs, fallbackTask: "do work", taskID: "task-1", parentSessionID: "sess-1"}
	text, ok := o.handleSubAgentCompactSession(context.Background(), ctx, "summary body", "model")
	if !ok || !strings.Contains(text, "Checkpoint 1") {
		t.Fatalf("unexpected result ok=%v text=%q", ok, text)
	}
	if len(msgs) >= 12 {
		t.Fatalf("messages not compacted: %d", len(msgs))
	}
}
