package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

type sequenceBridge struct {
	requests []bridge.Request
}

func (b *sequenceBridge) ID() string              { return "sequence" }
func (b *sequenceBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *sequenceBridge) Complete(_ context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.requests = append(b.requests, req)
	out := make(chan bridge.StreamEvent, 4)
	call := len(b.requests)
	go func() {
		defer close(out)
		if call == 1 {
			args, _ := json.Marshal(map[string]string{"task_id": "task-test"})
			tool := parse.ToolCall{Name: "sapaloq_get_task_status", Arguments: args}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &tool}
		} else {
			out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "continued"}
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

// thinkingBridge emits one reasoning delta then a response, with no tool calls.
type thinkingBridge struct{}

func (b *thinkingBridge) ID() string              { return "thinking" }
func (b *thinkingBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *thinkingBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent, 4)
	go func() {
		defer close(out)
		out <- bridge.StreamEvent{Kind: bridge.EventThinkingDelta, Delta: "let me reason "}
		out <- bridge.StreamEvent{Kind: bridge.EventThinkingDelta, Delta: "about this"}
		out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "final answer"}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

func TestRunConversationCapturesThinking(t *testing.T) {
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	snap := providerSnapshot{entry: config.LLMBridge{Key: "test", Model: "model"}, br: &thinkingBridge{}}
	out := make(chan bridge.StreamEvent, 16)
	go func() {
		for range out {
		}
	}()
	var thinking strings.Builder
	result, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "hi"}}, &thinking)
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	if result.String() != "final answer" {
		t.Fatalf("answer = %q, want %q", result.String(), "final answer")
	}
	if thinking.String() != "let me reason about this" {
		t.Fatalf("thinking = %q, want accumulated reasoning", thinking.String())
	}
}

func TestRunConversationContinuesAfterToolResult(t *testing.T) {
	fake := &sequenceBridge{}
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	if err := orch.writeTask(taskRecord{ID: "task-test", Status: "done", Result: "result"}); err != nil {
		t.Fatal(err)
	}
	snap := providerSnapshot{
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}
	out := make(chan bridge.StreamEvent, 16)
	go func() {
		for range out {
		}
	}()
	result, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "status"}}, nil)
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(fake.requests))
	}
	if result.String() != "continued" {
		t.Fatalf("result = %q", result.String())
	}
	if got := fake.requests[1].Messages[len(fake.requests[1].Messages)-1].Content; got == "" {
		t.Fatal("tool result was not sent back to model")
	}
}

// TestRunConversationInjectsUsageReadout verifies the continuation message sent
// back to the model carries a lightweight, informational usage readout (turn +
// tool-calls so far) so the model has self-awareness to pace its own work.
func TestRunConversationInjectsUsageReadout(t *testing.T) {
	fake := &sequenceBridge{}
	orch := &Orchestrator{
		memoryDir: t.TempDir(),
		vision:    make(map[string]bool),
		active:    make(map[string]*activeRun),
	}
	if err := orch.writeTask(taskRecord{ID: "task-test", Status: "done", Result: "result"}); err != nil {
		t.Fatal(err)
	}
	snap := providerSnapshot{
		cfg:   config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}
	out := make(chan bridge.StreamEvent, 16)
	go func() {
		for range out {
		}
	}()
	_, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "status"}}, nil)
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	// The 2nd request's last message is the continuation we built after turn 1
	// (which made exactly one tool call).
	got := fake.requests[1].Messages[len(fake.requests[1].Messages)-1].Content
	if !strings.Contains(got, "[Usage]") {
		t.Fatalf("continuation missing usage readout: %q", got)
	}
	if !strings.Contains(got, "tool-calls so far 1") {
		t.Fatalf("usage readout should report 1 tool call so far: %q", got)
	}
}

type longSequenceBridge struct {
	requests int
	tools    int
}

func (b *longSequenceBridge) ID() string              { return "long-sequence" }
func (b *longSequenceBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *longSequenceBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	b.requests++
	call := b.requests
	out := make(chan bridge.StreamEvent, 3)
	go func() {
		defer close(out)
		if call <= b.tools {
			args, _ := json.Marshal(map[string]string{"task_id": fmt.Sprintf("task-%d", call)})
			tool := parse.ToolCall{Name: "sapaloq_get_task_status", Arguments: args}
			out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &tool}
		} else {
			out <- bridge.StreamEvent{Kind: bridge.EventResponseDelta, Delta: "finished"}
		}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

func TestRunConversationSupportsMoreThanEightContinuations(t *testing.T) {
	fake := &longSequenceBridge{tools: 12}
	orch := &Orchestrator{memoryDir: t.TempDir(), vision: make(map[string]bool)}
	for i := 1; i <= fake.tools; i++ {
		if err := orch.writeTask(taskRecord{ID: fmt.Sprintf("task-%d", i), Status: "done", Result: fmt.Sprintf("result-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	snap := providerSnapshot{
		cfg:   config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    fake,
	}
	out := make(chan bridge.StreamEvent, 128)
	go func() {
		for range out {
		}
	}()
	result, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "run"}}, nil)
	close(out)
	if err != nil {
		t.Fatal(err)
	}
	if fake.requests != 13 {
		t.Fatalf("requests = %d, want 13", fake.requests)
	}
	if result.String() != "finished" {
		t.Fatalf("result = %q", result.String())
	}
}

func TestRunConversationStopsIdenticalToolLoop(t *testing.T) {
	orch := &Orchestrator{memoryDir: t.TempDir(), vision: make(map[string]bool)}
	if err := orch.writeTask(taskRecord{ID: "task-1", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	repeating := &repeatingToolBridge{}
	snap := providerSnapshot{
		cfg:   config.Config{Orchestrator: config.DefaultOrchestratorConfig()},
		entry: config.LLMBridge{Key: "test", Model: "model"},
		br:    repeating,
	}
	out := make(chan bridge.StreamEvent, 64)
	go func() {
		for range out {
		}
	}()
	_, err := orch.runConversation(context.Background(), snap, out, "session", "task", []bridge.Message{{Role: "user", Content: "loop"}}, nil)
	close(out)
	if err == nil || !strings.Contains(err.Error(), "identical tool call") {
		t.Fatalf("err = %v", err)
	}
}

type repeatingToolBridge struct{}

func (b *repeatingToolBridge) ID() string              { return "repeat" }
func (b *repeatingToolBridge) Caps() bridge.BridgeCaps { return bridge.BridgeCaps{Tools: true} }
func (b *repeatingToolBridge) Complete(_ context.Context, _ bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent, 2)
	go func() {
		defer close(out)
		args, _ := json.Marshal(map[string]string{"task_id": "task-1"})
		tool := parse.ToolCall{Name: "sapaloq_get_task_status", Arguments: args}
		out <- bridge.StreamEvent{Kind: bridge.EventToolCall, ToolCall: &tool}
		out <- bridge.StreamEvent{Kind: bridge.EventDone}
	}()
	return out, nil
}

func TestWaitForTaskChangeUsesBackendSignal(t *testing.T) {
	orch := &Orchestrator{memoryDir: t.TempDir()}
	now := time.Now().UTC()
	if err := orch.writeTask(taskRecord{ID: "task-wait", Status: "in_progress", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(25 * time.Millisecond)
		_ = orch.writeTask(taskRecord{ID: "task-wait", Status: "done", Result: "ok", CreatedAt: now, UpdatedAt: time.Now().UTC()})
	}()
	record, changed, err := orch.waitForTaskChange(context.Background(), "task-wait", 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || record.Status != "done" {
		t.Fatalf("changed=%v record=%#v", changed, record)
	}
}

// TestWaitIgnoresNonTerminalProgress proves the "blocking progress" fix: a bare
// progress update (UpdatedAt advances, status stays in_progress — e.g. the agent
// calling sapaloq_update_task_progress) must NOT break the wait. Otherwise the
// orchestrator returns "changed to in_progress", re-waits, and the chat freezes
// in a wait→progress→wait loop. The wait should run out its (short) window and
// report no meaningful change.
func TestWaitIgnoresNonTerminalProgress(t *testing.T) {
	orch := &Orchestrator{memoryDir: t.TempDir()}
	now := time.Now().UTC()
	if err := orch.writeTask(taskRecord{ID: "task-prog", Status: "in_progress", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Emit several progress bumps with the SAME status during the wait window.
	go func() {
		for i := 0; i < 3; i++ {
			time.Sleep(15 * time.Millisecond)
			_ = orch.writeTask(taskRecord{ID: "task-prog", Status: "in_progress", CreatedAt: now, UpdatedAt: time.Now().UTC()})
		}
	}()
	record, changed, err := orch.waitForTaskChange(context.Background(), "task-prog", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("wait broke on a non-terminal progress update; want changed=false (record=%#v)", record)
	}
	if record.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", record.Status)
	}
}

// TestWaitReturnsOnStatusTransition confirms a genuine non-terminal status
// transition (pending → in_progress) still ends the wait promptly.
func TestWaitReturnsOnStatusTransition(t *testing.T) {
	orch := &Orchestrator{memoryDir: t.TempDir()}
	now := time.Now().UTC()
	if err := orch.writeTask(taskRecord{ID: "task-trans", Status: "pending", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(25 * time.Millisecond)
		_ = orch.writeTask(taskRecord{ID: "task-trans", Status: "in_progress", CreatedAt: now, UpdatedAt: time.Now().UTC()})
	}()
	record, changed, err := orch.waitForTaskChange(context.Background(), "task-trans", 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || record.Status != "in_progress" {
		t.Fatalf("status transition did not end the wait: changed=%v record=%#v", changed, record)
	}
}

func TestCompactConversationPreservesGoalAndRecentMessages(t *testing.T) {
	messages := []bridge.Message{{Role: "system", Content: "system"}}
	for i := 0; i < 12; i++ {
		messages = append(messages, bridge.Message{Role: "user", Content: fmt.Sprintf("message-%d", i)})
	}
	compacted := compactConversationMessages(messages, "original goal", 0.30)
	if len(compacted) >= len(messages) {
		t.Fatalf("compaction did not reduce messages: %d >= %d", len(compacted), len(messages))
	}
	if !strings.Contains(compacted[1].Content, "original goal") {
		t.Fatalf("checkpoint does not preserve goal: %q", compacted[1].Content)
	}
	if compacted[len(compacted)-1].Content != "message-11" {
		t.Fatalf("latest message not preserved: %#v", compacted[len(compacted)-1])
	}
}

func TestExtractImagesBuildsVisionPayload(t *testing.T) {
	messages, images := extractImages([]bridge.Message{{
		Role:    "user",
		Content: "describe\n![sample](data:image/png;base64,aGVsbG8=)",
	}})
	if len(images) != 1 || images[0].MimeType != "image/png" {
		t.Fatalf("images = %#v", images)
	}
	if messages[0].Content == "" || messages[0].Content == "describe\n![sample](data:image/png;base64,aGVsbG8=)" {
		t.Fatalf("image marker was not replaced: %q", messages[0].Content)
	}
}
