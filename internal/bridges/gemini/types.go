package gemini

import "encoding/json"

const driverID = "gemini-bridge"

type functionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type functionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type content struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

type usageMetadata struct {
	ThoughtsTokenCount int `json:"thoughtsTokenCount"`
}

type candidate struct {
	Content      content `json:"content"`
	FinishReason string  `json:"finishReason"`
}

type response struct {
	Candidates    []candidate `json:"candidates"`
	UsageMetadata usageMetadata `json:"usageMetadata"`
	Error         *apiError   `json:"error,omitempty"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

type wireMetaPayload struct {
	Driver     string `json:"driver"`
	ModelParts []part `json:"model_parts"`
}

type turnAccum struct {
	thinking        string
	content         string
	toolCalls       []toolCallRecord
	modelParts      []part
	finishReason    string
	reasoningTokens int
}

type toolCallRecord struct {
	id        string
	name      string
	arguments string
}

type requestOptions struct {
	withToolChoice bool
	withReasoning  bool
}
