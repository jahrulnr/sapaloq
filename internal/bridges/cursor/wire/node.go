package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	thinkingcursor "github.com/jahrulnr/sapaloq/internal/parse/thinking/cursor"
)

type nodeStreamInput struct {
	Model           string        `json:"model"`
	Messages        []ChatMessage `json:"messages"`
	AccessToken     string        `json:"accessToken"`
	MachineID       string        `json:"machineId"`
	GhostMode       bool          `json:"ghostMode"`
	Tools           []any         `json:"tools,omitempty"`
	ForceAgentMode  bool          `json:"forceAgentMode,omitempty"`
	Instruction     string        `json:"instruction,omitempty"`
	ReasoningEffort string        `json:"reasoningEffort,omitempty"`
}

type nodeToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type nodeStreamOutput struct {
	OK        bool           `json:"ok"`
	Status    int            `json:"status"`
	Thinking  string         `json:"thinking"`
	Content   string         `json:"content"`
	ToolCalls []nodeToolCall `json:"toolCalls"`
	Error     string         `json:"error"`
}

// StreamChatNode calls cursor-proto-lab via Node (same stack as cursor-probe).
// Go's raw/http2 drivers are rejected by api2 with the same vscdb token Node accepts.
func StreamChatNode(ctx context.Context, opts StreamOptions, onFrame FrameHandler) error {
	script, err := nodeStreamScriptPath()
	if err != nil {
		return err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("node not found for cursor wire driver: %w", err)
	}

	in := nodeStreamInput{
		Model:           defaultIfEmpty(opts.Model, "default"),
		Messages:        opts.Messages,
		AccessToken:     opts.Token,
		MachineID:       opts.MachineID,
		GhostMode:       opts.GhostMode,
		Tools:           nodeToolsJSON(opts.Tools),
		ForceAgentMode:  opts.ForceAgentMode,
		Instruction:     opts.Instruction,
		ReasoningEffort: opts.ReasoningEffort,
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, node, script)
	cmd.Stdin = bytes.NewReader(body)
	if root := strings.TrimSpace(os.Getenv("SAPALOQ_CURSOR_BRIDGE_DIR")); root != "" {
		cmd.Env = append(os.Environ(), "SAPALOQ_CURSOR_BRIDGE_DIR="+root)
	} else {
		cmd.Env = os.Environ()
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if out, perr := parseNodeStreamOutput(stdout.Bytes()); perr == nil && out.Error != "" {
			return fmt.Errorf("cursor api error: %s", out.Error)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("cursor node stream failed: %s", msg)
	}

	out, err := parseNodeStreamOutput(stdout.Bytes())
	if err != nil {
		return err
	}
	if !out.OK {
		if out.Error != "" {
			return fmt.Errorf("cursor api error: %s", out.Error)
		}
		return fmt.Errorf("cursor node stream failed with status %d", out.Status)
	}
	thinking, content := out.Thinking, out.Content
	if content == "" && thinking != "" {
		parsed := thinkingcursor.ParseCursorThinking(thinking)
		thinking, content = parsed.Thinking, parsed.Response
	}
	if thinking != "" {
		onFrame(ExtractedPart{Thinking: thinking})
	}
	if content != "" {
		onFrame(ExtractedPart{Text: content})
	}
	for _, tc := range out.ToolCalls {
		if tc.ID == "" || tc.Function.Name == "" {
			continue
		}
		onFrame(ExtractedPart{ToolCall: &ToolCallPart{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		}})
	}
	// An empty api2 turn (no thinking, text, or tool calls) is not a hard
	// failure: the orchestrator treats tool-less turns as continuations and
	// nudges the model on the next inference turn (see subagent_stream_retry_test).
	return nil
}

func nodeToolsJSON(tools []MCPToolDecl) []any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		var params map[string]any
		_ = json.Unmarshal([]byte(tool.ParametersJSON), &params)
		if params == nil {
			params = map[string]any{"type": "object", "additionalProperties": true}
		}
		fn := map[string]any{
			"name":       tool.Name,
			"parameters": params,
		}
		if tool.Description != "" {
			fn["description"] = tool.Description
		}
		out = append(out, map[string]any{
			"type":     "function",
			"function": fn,
		})
	}
	return out
}

func parseNodeStreamOutput(raw []byte) (nodeStreamOutput, error) {
	var out nodeStreamOutput
	if err := json.Unmarshal(bytes.TrimSpace(raw), &out); err != nil {
		return out, fmt.Errorf("cursor node stream invalid json: %w", err)
	}
	return out, nil
}

func nodeStreamScriptPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("SAPALOQ_CURSOR_NODE_SCRIPT")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("SAPALOQ_CURSOR_NODE_SCRIPT not found: %s", p)
	}
	candidates := []string{
		"scripts/cursor-node-stream.mjs",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "share", "sapaloq", "cursor-node-stream.mjs"))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "..", "share", "sapaloq", "cursor-node-stream.mjs"))
	}
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			candidates = append(candidates, filepath.Join(dir, "scripts", "cursor-node-stream.mjs"))
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				break
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		c = filepath.Clean(c)
		if seen[c] || c == "" {
			continue
		}
		seen[c] = true
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("cursor-node-stream.mjs not found (set SAPALOQ_CURSOR_NODE_SCRIPT)")
}

func NodeStreamAvailable() bool {
	if _, err := nodeStreamScriptPath(); err != nil {
		return false
	}
	_, err := exec.LookPath("node")
	return err == nil
}
