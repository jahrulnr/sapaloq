package cursor

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
	"github.com/jahrulnr/sapaloq/internal/parse"
	"github.com/jahrulnr/sapaloq/internal/parse/artifacts"
	toolcursor "github.com/jahrulnr/sapaloq/internal/parse/tools/cursor"
	"github.com/jahrulnr/sapaloq/internal/parse/tools/kimi"
)

type bufferedProtoTool struct {
	id, name string
	args     strings.Builder
}

type liveTurnBuffer struct {
	totalThinking            strings.Builder
	totalContent             strings.Builder
	promotedThinking         strings.Builder
	promotedThinkingLen      int
	kimiBlockStarted         bool
	protoByID                map[string]*bufferedProtoTool
	protoOrder               []string
	frameCount               int
	kimiTokens               []string
	promoteThinking          bool
	mcpToolCompleted         bool
	mcpToolCount             int
}

func newLiveTurnBuffer(kimiTokens []string, promoteThinking bool) *liveTurnBuffer {
	return &liveTurnBuffer{
		protoByID:       map[string]*bufferedProtoTool{},
		kimiTokens:      kimiTokens,
		promoteThinking: promoteThinking,
	}
}

// noteMCPTool marks that an api5 in-bridge MCP tool ran on this turn. Post-MCP
// text/thinking on the agent wire is continuation noise, not assistant output.
func (acc *liveTurnBuffer) noteMCPTool() {
	acc.mcpToolCount++
	acc.mcpToolCompleted = true
}

func (acc *liveTurnBuffer) ingestAgentDecoded(d wire.AgentDecoded) {
	acc.frameCount++
	if acc.mcpToolCompleted {
		return
	}
	toolCallActive := len(acc.protoByID) > 0 || len(acc.protoOrder) > 0 || acc.mcpToolCount > 0

	switch d.Kind {
	case "thinking":
		if d.Thinking == "" || ShouldSuppressKimiToolStreamChunk(d.Thinking, acc.kimiTokens) {
			return
		}
		acc.totalThinking.WriteString(d.Thinking)
		if acc.promoteThinking {
			acc.promotedThinking.WriteString(d.Thinking)
			if visible := VisibleContentFromThinking(acc.promotedThinking.String()); len(visible) > acc.promotedThinkingLen {
				delta := visible[acc.promotedThinkingLen:]
				acc.promotedThinkingLen = len(visible)
				acc.appendVisibleDelta(delta, toolCallActive)
			}
		}
	case "text":
		if d.Text != "" {
			acc.appendVisibleDelta(d.Text, toolCallActive)
		}
	}
}

func (acc *liveTurnBuffer) ingest(part wire.ExtractedPart) {
	acc.frameCount++
	if acc.mcpToolCompleted {
		return
	}
	toolCallActive := len(acc.protoByID) > 0 || len(acc.protoOrder) > 0

	if part.Thinking != "" && !ShouldSuppressKimiToolStreamChunk(part.Thinking, acc.kimiTokens) {
		acc.totalThinking.WriteString(part.Thinking)
		if acc.promoteThinking {
			acc.promotedThinking.WriteString(part.Thinking)
			if visible := VisibleContentFromThinking(acc.promotedThinking.String()); len(visible) > acc.promotedThinkingLen {
				delta := visible[acc.promotedThinkingLen:]
				acc.promotedThinkingLen = len(visible)
				acc.appendVisibleDelta(delta, toolCallActive)
			}
		}
	}

	if part.Text != "" {
		acc.appendVisibleDelta(part.Text, toolCallActive)
	}

	if part.ToolCall != nil {
		acc.mergeProtoTool(part.ToolCall)
	}
}

func (acc *liveTurnBuffer) appendVisibleDelta(delta string, toolCallActive bool) {
	if shouldStreamCursorContentDelta(delta, acc.totalContent.String(), acc.kimiBlockStarted, toolCallActive, acc.kimiTokens) {
		acc.totalContent.WriteString(delta)
	} else {
		acc.totalContent.WriteString(delta)
		if kimi.ToolBlockActive(acc.totalContent.String(), acc.kimiTokens) {
			acc.kimiBlockStarted = true
		}
	}
}

func (acc *liveTurnBuffer) mergeProtoTool(tc *wire.ToolCallPart) {
	if tc == nil || tc.ID == "" {
		return
	}
	existing, ok := acc.protoByID[tc.ID]
	if !ok {
		existing = &bufferedProtoTool{id: tc.ID, name: tc.Name}
		acc.protoByID[tc.ID] = existing
		acc.protoOrder = append(acc.protoOrder, tc.ID)
	}
	if tc.Name != "" {
		existing.name = tc.Name
	}
	if tc.Arguments != "" {
		existing.args.WriteString(tc.Arguments)
	}
}

func (acc *liveTurnBuffer) thinkingText() string {
	return acc.totalThinking.String()
}

func (acc *liveTurnBuffer) contentText() string {
	return acc.totalContent.String()
}

func (b *Bridge) finalizeBufferedTurn(
	ctx context.Context,
	out chan<- bridge.StreamEvent,
	sessionID string,
	declared []string,
	guard GuardContext,
	userPrompt string,
	acc *liveTurnBuffer,
) (responseBytes int, noiseDropped bool) {
	if acc == nil {
		return 0, false
	}

	protoCalls := acc.finalizeProtoToolCalls()
	kimiCalls := acc.extractKimiInlineTools(len(protoCalls))
	allCalls := append(append([]parse.ToolCall(nil), protoCalls...), kimiCalls...)
	toolCallCount := len(allCalls)
	if acc.mcpToolCount > toolCallCount {
		toolCallCount = acc.mcpToolCount
	}

	thinking := acc.thinkingText()
	content := acc.contentText()
	noiseTurn := false
	if thinking != "" {
		if guard.ForceAgentMode {
			// Agent: drop only hard cross-session bleed; keep task narration so tools can follow.
			noiseTurn = artifacts.IsThinkingConfabulation(thinking)
		} else {
			noiseTurn = artifacts.IsUnanchoredThinkingConfabulation(thinking, userPrompt)
		}
	}
	if noiseTurn {
		thinking = ""
		content = ""
		if len(allCalls) == 0 {
			noiseDropped = true
		}
	}

	if thinking != "" {
		ev := bridge.NewEvent(bridge.EventThinkingDelta)
		ev.SessionID = sessionID
		ev.Delta = thinking
		send(ctx, out, ev)
	}

	content = CleanKimiAssistantContent(content, acc.kimiTokens)
	content = FinalizeAssistantContentWithToolCalls(content, toolCallCount)
	content = b.schema.SanitizeFinalTurnContent(content, guard, toolCallCount)
	if content != "" && artifacts.IsModelResponseArtifact(content) {
		content = ""
	}

	if content != "" {
		ev := bridge.NewEvent(bridge.EventResponseDelta)
		ev.SessionID = sessionID
		ev.Delta = content
		send(ctx, out, ev)
	}

	for _, call := range allCalls {
		b.tryEmitToolCall(ctx, out, sessionID, declared, call)
	}

	return len(content), noiseDropped
}

func (acc *liveTurnBuffer) finalizeProtoToolCalls() []parse.ToolCall {
	out := make([]parse.ToolCall, 0, len(acc.protoOrder))
	for _, id := range acc.protoOrder {
		bt := acc.protoByID[id]
		if bt == nil || bt.name == "" {
			continue
		}
		raw, _ := json.Marshal(map[string]any{
			"id":        bt.id,
			"name":      bt.name,
			"arguments": json.RawMessage(bt.args.String()),
		})
		if call, ok := toolcursor.ParseClientSideToolV2Call(raw); ok {
			out = append(out, call)
		}
	}
	return out
}

func (acc *liveTurnBuffer) extractKimiInlineTools(protoCount int) []parse.ToolCall {
	if protoCount > 0 || !kimi.ToolBlockActive(acc.contentText(), acc.kimiTokens) {
		return nil
	}
	return kimi.ExtractWithTokens(acc.contentText(), acc.kimiTokens).Calls
}
