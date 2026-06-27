package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

type summaryBridge struct{}

func (summaryBridge) ID() string              { return "summary" }
func (summaryBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{} }
func (summaryBridge) Complete(ctx context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	ch := make(chan bridge.StreamEvent, 2)
	go func() {
		defer close(ch)
		ch <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "## Goals\n- ship feature", At: time.Now().UTC()}
	}()
	return ch, nil
}

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

func TestRunSubAgentCompact(t *testing.T) {
	o := &Orchestrator{cfg: config.Config{Orchestrator: config.DefaultOrchestratorConfig()}}
	msgs := []bridge.Message{
		{Role: "system", Content: "persona"},
		{Role: "user", Content: "do work"},
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs, bridge.Message{Role: "assistant", Content: "progress"})
	}
	ctx := &subAgentCompactCtx{messages: &msgs, fallbackTask: "do work", taskID: "task-1", parentSessionID: "sess-1"}
	snap := providerSnapshot{
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    summaryBridge{},
		cfg:   o.cfg,
	}
	if err := o.runSubAgentCompact(context.Background(), snap, ctx, "force_headroom"); err != nil {
		t.Fatal(err)
	}
	if len(msgs) >= 12 {
		t.Fatalf("messages not compacted: %d", len(msgs))
	}
	if ctx.checkpointIndex != 1 {
		t.Fatalf("checkpointIndex = %d, want 1", ctx.checkpointIndex)
	}
}
