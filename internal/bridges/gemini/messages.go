package gemini

import (
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	provider "github.com/jahrulnr/sapaloq/internal/bridges/provider"
	"github.com/jahrulnr/sapaloq/internal/config"
)

func buildFunctionDeclarations(names []string) []map[string]any {
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		schema := provider.RegisteredToolSchema(name)
		params := map[string]any{"type": "object", "additionalProperties": true}
		if len(schema) > 0 {
			_ = json.Unmarshal(schema, &params)
		}
		out = append(out, map[string]any{
			"name":        name,
			"description": provider.RegisteredToolDescription(name),
			"parameters":  params,
		})
	}
	return out
}

func buildRequestBody(entry config.LLMBridge, messages []bridge.Message, declaredTools []string, opts requestOptions) ([]byte, error) {
	contents, systemText := messagesToContents(messages)
	payload := map[string]any{"contents": contents}
	if systemText != "" {
		payload["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": systemText}},
		}
	}
	if len(declaredTools) > 0 {
		payload["tools"] = []any{map[string]any{
			"functionDeclarations": buildFunctionDeclarations(declaredTools),
		}}
	}
	if opts.withToolChoice {
		payload["toolConfig"] = map[string]any{
			"functionCallingConfig": map[string]any{"mode": "AUTO"},
		}
	}
	if opts.withReasoning {
		payload["generationConfig"] = map[string]any{
			"thinkingConfig": map[string]any{
				"thinkingLevel":   reasoningEffort(entry),
				"includeThoughts": true,
			},
		}
	}
	return json.Marshal(payload)
}

func messagesToContents(messages []bridge.Message) ([]content, string) {
	var out []content
	var systemParts []string
	var pendingToolNames []string

	flushUser := func(role string, parts []part) {
		if len(parts) == 0 {
			return
		}
		out = append(out, content{Role: role, Parts: parts})
	}

	var userParts []part
	appendUserText := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		userParts = append(userParts, part{Text: text})
	}

	for _, msg := range messages {
		role := msg.Role
		switch role {
		case "system":
			if body := strings.TrimSpace(msg.Content); body != "" {
				systemParts = append(systemParts, body)
			}
		case "assistant":
			if len(userParts) > 0 {
				flushUser("user", userParts)
				userParts = nil
			}
			if payload, ok := decodeWireMeta(msg.WireMeta); ok {
				pendingToolNames = functionCallNames(payload.ModelParts)
				out = append(out, content{Role: "model", Parts: payload.ModelParts})
				continue
			}
			pendingToolNames = nil
			if body := strings.TrimSpace(msg.Content); body != "" {
				out = append(out, content{Role: "model", Parts: []part{{Text: body}}})
			}
		case "tool":
			name := "tool"
			if len(pendingToolNames) > 0 {
				name = pendingToolNames[0]
				pendingToolNames = pendingToolNames[1:]
			}
			if len(userParts) > 0 {
				flushUser("user", userParts)
				userParts = nil
			}
			out = append(out, content{Role: "user", Parts: []part{{
				FunctionResponse: &functionResponse{
					Name:     name,
					Response: toolResultResponse(msg.Content),
				},
			}}})
		case "user":
			appendUserText(msg.Content)
		default:
			appendUserText(msg.Content)
		}
	}
	if len(userParts) > 0 {
		flushUser("user", userParts)
	}
	return out, strings.Join(systemParts, "\n\n")
}

func toolResultResponse(body string) json.RawMessage {
	body = strings.TrimSpace(body)
	if body == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(body)) {
		return json.RawMessage(body)
	}
	b, _ := json.Marshal(map[string]string{"output": body})
	return b
}
