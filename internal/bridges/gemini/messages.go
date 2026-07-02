package gemini

import (
	"encoding/base64"
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
		params := map[string]any{"type": "object"}
		if len(schema) > 0 {
			_ = json.Unmarshal(schema, &params)
		}
		stripAdditionalProperties(params)
		out = append(out, map[string]any{
			"name":        name,
			"description": provider.RegisteredToolDescription(name),
			"parameters":  params,
		})
	}
	return out
}

// stripAdditionalProperties recursively removes "additionalProperties" from
// the parameters map and any nested property schemas, including those inside
// "items" arrays. Gemini's API rejects this field with a 400 error at any level.
func stripAdditionalProperties(m map[string]any) {
	delete(m, "additionalProperties")
	if props, ok := m["properties"].(map[string]any); ok {
		for _, v := range props {
			if child, ok := v.(map[string]any); ok {
				stripAdditionalProperties(child)
			}
		}
	}
	if items, ok := m["items"].(map[string]any); ok {
		stripAdditionalProperties(items)
	}
}

func buildRequestBody(entry config.LLMBridge, messages []bridge.Message, declaredTools []string, opts requestOptions, images []bridge.Image) ([]byte, error) {
	contents, systemText := messagesToContents(messages, images)
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

func messagesToContents(messages []bridge.Message, images []bridge.Image) ([]content, string) {
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
	var userPartIdx int
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
			userPartIdx++
		default:
			appendUserText(msg.Content)
			userPartIdx++
		}
	}
	// Attach inline images to the final user message before flushing.
	if len(userParts) > 0 && len(images) > 0 {
		for _, img := range images {
			if d := imageToInlineData(img); d != nil {
				userParts = append(userParts, part{InlineData: d})
			}
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

// imageToInlineData converts a bridge.Image to Gemini's inlineData format.
// Accepts either a data URI (data:<mime>;base64,<payload>) or raw bytes +
// MimeType. Returns nil when the image is unusable.
func imageToInlineData(img bridge.Image) *inlineData {
	mime, b64 := decodeImageDataURI(img.DataURI)
	if b64 == "" && len(img.Data) > 0 && img.MimeType != "" {
		mime = img.MimeType
		b64 = base64.StdEncoding.EncodeToString(img.Data)
	}
	if b64 == "" || mime == "" {
		return nil
	}
	return &inlineData{MimeType: mime, Data: b64}
}

// decodeImageDataURI parses a data URI (data:<mime>;base64,<payload>) and
// returns the mime type and base64 payload. Returns empty strings on failure.
func decodeImageDataURI(uri string) (mime, b64 string) {
	const prefix = "data:"
	if !strings.HasPrefix(uri, prefix) {
		return "", ""
	}
	rest := uri[len(prefix):]
	semi := strings.IndexByte(rest, ';')
	if semi < 0 {
		return "", ""
	}
	mime = rest[:semi]
	rest = rest[semi+1:]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", ""
	}
	if rest[:comma] != "base64" {
		return "", ""
	}
	return mime, rest[comma+1:]
}
