package provider

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	toolprovider "github.com/jahrulnr/sapaloq/internal/parse/tools/provider"
)

// openAIRequest is the body for Chat Completions and OpenAI-compatible APIs.
type openAIRequest struct {
	Model               string          `json:"model"`
	Stream              bool            `json:"stream"`
	Messages            []openAIMessage `json:"messages"`
	Tools               []openAITool    `json:"tools,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	ExtraBody           map[string]any  `json:"-"` // merged into body via custom marshal below
}

func (r openAIRequest) MarshalJSON() ([]byte, error) {
	type alias openAIRequest
	base, err := json.Marshal(alias(r))
	if err != nil {
		return nil, err
	}
	if len(r.ExtraBody) == 0 {
		return base, nil
	}
	var merged map[string]any
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range r.ExtraBody {
		merged[k] = v
	}
	return json.Marshal(merged)
}

type openAIMessage struct {
	Role    string        `json:"role"`
	Content openAIContent `json:"content"`
}

type openAIContent []openAIPart

func (c openAIContent) MarshalJSON() ([]byte, error) {
	if len(c) == 1 && c[0].Type == "text" {
		return json.Marshal(c[0].Text)
	}
	return json.Marshal([]openAIPart(c))
}

func (c *openAIContent) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*c = openAIContent{{Type: "text", Text: s}}
		return nil
	}
	var arr []openAIPart
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	*c = arr
	return nil
}

type openAIPart struct {
	Type     string     `json:"type"`
	Text     string     `json:"text,omitempty"`
	ImageURL *openAIImg `json:"image_url,omitempty"`
}

type openAIImg struct {
	URL string `json:"url"`
}

type openAITool struct {
	Type     string     `json:"type"`
	Function openAIFunc `json:"function"`
}

type openAIFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// openAIChunk is a streaming response chunk.
type openAIChunk struct {
	ID      string         `json:"id,omitempty"`
	Object  string         `json:"object,omitempty"`
	Choices []openAIChoice `json:"choices,omitempty"`
}

type openAIChoice struct {
	Index        int         `json:"index"`
	Delta        openAIDelta `json:"delta"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

type openAIDelta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	Reasoning string          `json:"reasoning_content,omitempty"`
	ToolCalls []openAIToolDel `json:"tool_calls,omitempty"`
}

type openAIToolDel struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// openAIToToolDelta converts the streaming chunk's tool-call entry to the
// parser package's delta type.
func openAIToToolDelta(tc openAIToolDel) toolprovider.OpenAIToolDelta {
	return toolprovider.OpenAIToolDelta{
		Index: tc.Index,
		ID:    tc.ID,
		Type:  tc.Type,
		Function: struct {
			Name      string `json:"name,omitempty"`
			Arguments string `json:"arguments,omitempty"`
		}{
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		},
	}
}

// claudeRequest is the body for Anthropic Messages API.
type claudeRequest struct {
	Model     string          `json:"model"`
	Stream    bool            `json:"stream"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []claudeMessage `json:"messages"`
	Tools     []claudeTool    `json:"tools,omitempty"`
	Thinking  *claudeThinking `json:"thinking,omitempty"`
}

type claudeMessage struct {
	Role    string       `json:"role"`
	Content []claudePart `json:"content"`
}

type claudePart struct {
	Type   string           `json:"type"` // text | image | tool_use | tool_result
	Text   string           `json:"text,omitempty"`
	Source *claudeImgSource `json:"source,omitempty"`
	ID     string           `json:"id,omitempty"`
	Name   string           `json:"name,omitempty"`
	Input  json.RawMessage  `json:"input,omitempty"`
}

type claudeImgSource struct {
	Type      string `json:"type"` // base64
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type claudeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type claudeThinking struct {
	Type         string `json:"type"` // enabled | disabled | adaptive
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	Display      string `json:"display,omitempty"`
}

// claudeThinkingFromEffort maps the high-level effort tier to the Anthropic
// extended-thinking parameters.
func claudeThinkingFromEffort(effort string) *claudeThinking {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return &claudeThinking{Type: "enabled", BudgetTokens: 1024}
	case "medium":
		return &claudeThinking{Type: "enabled", BudgetTokens: 5000}
	case "high":
		return &claudeThinking{Type: "enabled", BudgetTokens: 16000}
	case "disabled":
		return &claudeThinking{Type: "disabled"}
	default:
		tokens, err := strconv.Atoi(effort)
		if err != nil || tokens <= 0 {
			return nil
		}
		return &claudeThinking{Type: "enabled", BudgetTokens: tokens}
	}
}

// buildOpenAIMessages converts bridge.Message into the OpenAI / Kimi request
// shape, attaching any inline images as content parts.
func buildOpenAIMessages(messages []bridge.Message, images []bridge.Image) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for i, msg := range messages {
		parts := openAIPartsForMessage(msg, shouldAttachOpenAIImages(i, len(messages), msg.Role, images), images)
		out = append(out, openAIMessage{Role: msg.Role, Content: parts})
	}
	return out
}

// shouldAttachOpenAIImages returns true when inline images should be attached
// to this message — only the final user message gets them.
func shouldAttachOpenAIImages(idx, total int, role string, images []bridge.Image) bool {
	return idx == total-1 && role == "user" && len(images) > 0
}

// openAIPartsForMessage returns the content parts for a single message,
// optionally appending image_url entries when `attachImages` is true.
func openAIPartsForMessage(msg bridge.Message, attachImages bool, images []bridge.Image) openAIContent {
	parts := make(openAIContent, 0, 2)
	if msg.Content != "" {
		parts = append(parts, openAIPart{Type: "text", Text: msg.Content})
	}
	if !attachImages {
		return parts
	}
	for _, img := range images {
		uri := openAIDataURI(img)
		if uri == "" {
			continue
		}
		parts = append(parts, openAIPart{
			Type:     "image_url",
			ImageURL: &openAIImg{URL: uri},
		})
	}
	return parts
}

// openAIDataURI returns the data URI for an image, building one from raw
// bytes + mime type when DataURI is empty. Returns "" when the image is
// unusable.
func openAIDataURI(img bridge.Image) string {
	if img.DataURI != "" {
		return img.DataURI
	}
	if len(img.Data) == 0 || img.MimeType == "" {
		return ""
	}
	return fmt.Sprintf("data:%s;base64,%s", img.MimeType, string(img.Data))
}

// buildClaudeMessages converts bridge.Message into Claude request shape. The
// optional system prompt is separated into claudeRequest.System.
func buildClaudeMessages(messages []bridge.Message, images []bridge.Image) []claudeMessage {
	out := make([]claudeMessage, 0, len(messages))
	for i, msg := range messages {
		parts := claudePartsForMessage(msg, shouldAttachClaudeImages(i, len(messages), msg.Role, images), images)
		out = append(out, claudeMessage{Role: msg.Role, Content: parts})
	}
	return out
}

// shouldAttachClaudeImages mirrors shouldAttachOpenAIImages for the Claude path.
func shouldAttachClaudeImages(idx, total int, role string, images []bridge.Image) bool {
	return idx == total-1 && role == "user" && len(images) > 0
}

// claudePartsForMessage returns the content parts for a single Claude message.
func claudePartsForMessage(msg bridge.Message, attachImages bool, images []bridge.Image) []claudePart {
	parts := make([]claudePart, 0, 2)
	if msg.Content != "" {
		parts = append(parts, claudePart{Type: "text", Text: msg.Content})
	}
	if !attachImages {
		return parts
	}
	for _, img := range images {
		uri := openAIDataURI(img)
		if uri == "" {
			continue
		}
		parts = append(parts, claudePart{
			Type: "image",
			Source: &claudeImgSource{
				Type:      "base64",
				MediaType: img.MimeType,
				Data:      extractBase64(uri),
			},
		})
	}
	return parts
}

// buildOpenAITools converts the declared tool name list into OpenAI tool
// definitions. We don't have per-tool JSON schemas, so we accept any
// parameters object.
func buildOpenAITools(names []string) []openAITool {
	out := make([]openAITool, 0, len(names))
	for _, name := range names {
		out = append(out, openAITool{
			Type: "function",
			Function: openAIFunc{
				Name:       name,
				Parameters: toolSchemaFor(name),
			},
		})
	}
	return out
}

// buildClaudeTools converts the declared tool name list into Claude tool
// definitions.
func buildClaudeTools(names []string) []claudeTool {
	out := make([]claudeTool, 0, len(names))
	for _, name := range names {
		out = append(out, claudeTool{
			Name:        name,
			InputSchema: toolSchemaFor(name),
		})
	}
	return out
}

// extractBase64 strips the data:<mime>;base64, prefix from a data URI.
func extractBase64(uri string) string {
	const marker = ";base64,"
	idx := strings.Index(uri, marker)
	if idx < 0 {
		return uri
	}
	return uri[idx+len(marker):]
}
