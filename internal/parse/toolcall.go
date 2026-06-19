package parse

import "encoding/json"

type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Source    string          `json:"source,omitempty"`
}

func NewToolCall(name string, args json.RawMessage, source string) ToolCall {
	return ToolCall{Name: name, Arguments: args, Source: source}
}
