package cursor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestNormalizeWebSearchSearchTerm(t *testing.T) {
	raw := json.RawMessage(`{"search_term":"amadna.com checkout","explanation":"test"}`)
	got := NormalizeToolCallArguments("web_search", raw)
	var obj map[string]string
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["query"] != "amadna.com checkout" {
		t.Fatalf("query = %q", obj["query"])
	}
}

func TestFinalizeBufferedTurnDropsUnanchoredNoise(t *testing.T) {
	dir := t.TempDir()
	entry, _ := defaultTestEntry()
	entry.DeclaredTools = []string{"web_search"}
	b, err := New(entry, config.RuntimeConfig{DataDir: dir, BinaryName: "sapaloq-core"})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	guard := schema.BuildGuardContext("claude-4.5-sonnet", entry.DeclaredTools, nil)
	acc := newLiveTurnBuffer(schema.KimiTokens(), false)
	acc.totalThinking.WriteString(`The user is encountering an error in a Terraform AWS configuration. The error is:

` + "```" + `
Error: Invalid Parameter Combination
` + "```" + `

Let me search for relevant files in the workspace.`)

	out := make(chan bridge.StreamEvent, 16)
	responseBytes, noiseDropped := b.finalizeBufferedTurn(context.Background(), out, "s-noise", entry.DeclaredTools, guard, "buat web keren di /tmp/profile", acc)
	close(out)

	var thinking, response string
	var toolCount int
	for ev := range out {
		switch ev.Kind {
		case bridge.EventThinkingDelta:
			thinking += ev.Delta
		case bridge.EventResponseDelta:
			response += ev.Delta
		case bridge.EventToolCall:
			toolCount++
		}
	}
	if !noiseDropped {
		t.Fatal("expected noiseDropped on terraform bleed turn")
	}
	if thinking != "" {
		t.Fatalf("unexpected thinking on noise turn: %q", thinking)
	}
	if toolCount != 0 {
		t.Fatalf("tool calls = %d want 0 on unanchored noise turn", toolCount)
	}
	if response != "" {
		t.Fatalf("unexpected response %q", response)
	}
	if responseBytes != 0 {
		t.Fatalf("responseBytes = %d want 0", responseBytes)
	}
}

func TestFinalizeDefersKimiToolsUntilEnd(t *testing.T) {
	dir := t.TempDir()
	entry, _ := defaultTestEntry()
	entry.DeclaredTools = []string{"glob"}
	b, err := New(entry, config.RuntimeConfig{DataDir: dir, BinaryName: "sapaloq-core"})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	guard := schema.BuildGuardContext("default", entry.DeclaredTools, nil)
	acc := newLiveTurnBuffer(schema.KimiTokens(), false)
	acc.totalContent.WriteString(`<｜tool▁call▁begin｜>glob<|tool_sep|>glob_pattern
*.go<｜tool▁call▁end｜>`)

	out := make(chan bridge.StreamEvent, 8)
	b.finalizeBufferedTurn(context.Background(), out, "s-tool", entry.DeclaredTools, guard, "find go files", acc)
	close(out)

	var toolName string
	for ev := range out {
		if ev.Kind == bridge.EventToolCall && ev.ToolCall != nil {
			toolName = ev.ToolCall.Name
		}
	}
	if toolName != "glob" {
		t.Fatalf("tool = %q want glob", toolName)
	}
}

func TestFinalizeDropsShortPreToolNarration(t *testing.T) {
	dir := t.TempDir()
	entry, _ := defaultTestEntry()
	entry.DeclaredTools = []string{"glob"}
	b, err := New(entry, config.RuntimeConfig{DataDir: dir, BinaryName: "sapaloq-core"})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	guard := schema.BuildGuardContext("default", entry.DeclaredTools, nil)
	acc := newLiveTurnBuffer(schema.KimiTokens(), false)
	acc.totalContent.WriteString(`I'll search now.<｜tool▁call▁begin｜>glob<|tool_sep|>glob_pattern
*.go<｜tool▁call▁end｜>`)

	out := make(chan bridge.StreamEvent, 8)
	b.finalizeBufferedTurn(context.Background(), out, "s-short", entry.DeclaredTools, guard, "find go files", acc)
	close(out)

	var response string
	for ev := range out {
		if ev.Kind == bridge.EventResponseDelta {
			response += ev.Delta
		}
	}
	if response != "" {
		t.Fatalf("short pre-tool narration should be dropped, got %q", response)
	}
}

func TestFinalizeBufferedTurnPreservesToolsOnNoiseWhenPresent(t *testing.T) {
	dir := t.TempDir()
	entry, _ := defaultTestEntry()
	entry.DeclaredTools = []string{"web_search"}
	b, err := New(entry, config.RuntimeConfig{DataDir: dir, BinaryName: "sapaloq-core"})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	guard := schema.BuildGuardContext("claude-4.5-sonnet", entry.DeclaredTools, nil)
	acc := newLiveTurnBuffer(schema.KimiTokens(), false)
	acc.totalThinking.WriteString(`The user is encountering an error in a Terraform AWS configuration.`)
	acc.totalContent.WriteString(`<｜tool▁call▁begin｜>web_search<|tool_sep|>search_term
amadna.com checkout<｜tool▁call▁end｜>`)

	out := make(chan bridge.StreamEvent, 16)
	_, noiseDropped := b.finalizeBufferedTurn(context.Background(), out, "s-noise", entry.DeclaredTools, guard, "buat web keren di /tmp/profile", acc)
	close(out)

	var toolCount int
	for ev := range out {
		if ev.Kind == bridge.EventToolCall {
			toolCount++
		}
	}
	if toolCount != 1 {
		t.Fatalf("tool calls = %d want 1 preserved despite noise thinking", toolCount)
	}
	if noiseDropped {
		t.Fatal("noiseDropped should be false when tools are present")
	}
}

func TestFinalizeAgentModeKeepsTaskNarrationThinking(t *testing.T) {
	dir := t.TempDir()
	entry, _ := defaultTestEntry()
	b, err := New(entry, config.RuntimeConfig{DataDir: dir, BinaryName: "sapaloq-core"})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	guard := schema.BuildGuardContext("default", nil, nil)
	acc := newLiveTurnBuffer(schema.KimiTokens(), true)
	acc.totalThinking.WriteString(`The user wants to fix devlog_front_widgets on the front page. Found the root cause in preprocess_page.`)

	out := make(chan bridge.StreamEvent, 8)
	b.finalizeBufferedTurn(context.Background(), out, "s-agent", entry.DeclaredTools, guard, "fix devlog front page widgets", acc)
	close(out)

	var thinking string
	for ev := range out {
		if ev.Kind == bridge.EventThinkingDelta {
			thinking += ev.Delta
		}
	}
	if thinking == "" {
		t.Fatal("agent mode should keep anchored task narration in thinking")
	}
}

func TestAgentTurnBufferDropsPostMCPEcho(t *testing.T) {
	dir := t.TempDir()
	entry, _ := defaultTestEntry()
	b, err := New(entry, config.RuntimeConfig{DataDir: dir, BinaryName: "sapaloq-core"})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	guard := schema.BuildGuardContext("default", entry.DeclaredTools, []bridge.Message{{Role: "user", Content: "hay hay"}})
	acc := newLiveTurnBuffer(schema.KimiTokens(), false)

	acc.ingestAgentDecoded(wire.AgentDecoded{Kind: "text", Text: "Hai! Ada yang bisa kubantu hari ini?"})
	acc.noteMCPTool()
	echo := "<user_query>\n[system]\n# SapaLOQ - persona\nYou are SapaLOQ.\n\n[user]\nhay hay"
	acc.ingestAgentDecoded(wire.AgentDecoded{Kind: "text", Text: echo})
	acc.ingestAgentDecoded(wire.AgentDecoded{Kind: "thinking", Thinking: echo})

	out := make(chan bridge.StreamEvent, 8)
	b.finalizeBufferedTurn(context.Background(), out, "s-agent", entry.DeclaredTools, guard, "hay hay", acc)
	close(out)

	var response string
	for ev := range out {
		if ev.Kind == bridge.EventResponseDelta {
			response += ev.Delta
		}
	}
	if strings.Contains(response, "[system]") || strings.Contains(response, "SapaLOQ - persona") {
		t.Fatalf("post-MCP echo leaked into response: %q", response)
	}
}

func TestAgentTurnBufferKeepsLongPreToolReply(t *testing.T) {
	dir := t.TempDir()
	entry, _ := defaultTestEntry()
	b, err := New(entry, config.RuntimeConfig{DataDir: dir, BinaryName: "sapaloq-core"})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	guard := schema.BuildGuardContext("default", entry.DeclaredTools, nil)
	acc := newLiveTurnBuffer(schema.KimiTokens(), false)

	longReply := strings.Repeat("Here is a detailed answer with enough length. ", 5)
	acc.ingestAgentDecoded(wire.AgentDecoded{Kind: "text", Text: longReply})
	acc.noteMCPTool()
	acc.ingestAgentDecoded(wire.AgentDecoded{Kind: "text", Text: "<user_query>\n[system]\n# SapaLOQ - persona\nleak"})

	out := make(chan bridge.StreamEvent, 8)
	b.finalizeBufferedTurn(context.Background(), out, "s-agent", entry.DeclaredTools, guard, "question", acc)
	close(out)

	var response string
	for ev := range out {
		if ev.Kind == bridge.EventResponseDelta {
			response += ev.Delta
		}
	}
	if !strings.Contains(response, "detailed answer") {
		t.Fatalf("pre-tool reply missing: %q", response)
	}
	if strings.Contains(response, "[system]") {
		t.Fatalf("post-MCP echo leaked: %q", response)
	}
}

func TestCoerceWebSearchToolCall(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	call := parse.NewToolCall("web_search", json.RawMessage(`{"search_term":"sapaloq docs"}`), "kimi_inline")
	coerced := CoerceToolCall(schema, call)
	var obj map[string]string
	if err := json.Unmarshal(coerced.Arguments, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["query"] != "sapaloq docs" {
		t.Fatalf("query = %q", obj["query"])
	}
}
