package cursor

import (
	"html"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
)

const cursorSystemInstructionsPrefix = "[System Instructions]\n"

// normalizeCursorWireMessages maps OpenAI-style roles to the Cursor api2 wire
// shape used by 9router/open-sse. cursor-proto-lab treats any non-user role as
// assistant; sending orchestrator.md as system would appear as fake assistant turns and
// trigger agent-task confabulation on short user messages like "hey hey".
func normalizeCursorWireMessages(messages []bridge.Message) []wire.ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]wire.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		content := msg.Content
		switch role {
		case "system":
			if strings.TrimSpace(content) == "" {
				continue
			}
			out = append(out, wire.ChatMessage{
				Role:    "user",
				Content: cursorSystemInstructionsPrefix + content,
			})
		case "tool":
			if strings.TrimSpace(content) == "" {
				continue
			}
			out = append(out, wire.ChatMessage{
				Role:    "user",
				Content: buildCursorToolResultBlock("tool", "", content),
			})
		case "user", "assistant":
			out = append(out, wire.ChatMessage{Role: role, Content: content})
		default:
			if strings.TrimSpace(content) == "" {
				continue
			}
			out = append(out, wire.ChatMessage{Role: "user", Content: content})
		}
	}
	return out
}

func buildCursorToolResultBlock(toolName, toolCallID, resultText string) string {
	clean := sanitizeCursorToolResultText(resultText)
	return strings.Join([]string{
		"<tool_result>",
		"<tool_name>" + escapeCursorXML(toolName) + "</tool_name>",
		"<tool_call_id>" + escapeCursorXML(toolCallID) + "</tool_call_id>",
		"<result>" + escapeCursorXML(clean) + "</result>",
		"</tool_result>",
	}, "\n")
}

func sanitizeCursorToolResultText(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if r == 0 || (r >= 1 && r <= 8) || r == 11 || r == 12 || (r >= 14 && r <= 31) || r == 127 {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func escapeCursorXML(text string) string {
	return html.EscapeString(text)
}
