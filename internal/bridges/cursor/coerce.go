package cursor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
	"github.com/jahrulnr/sapaloq/internal/parse/tools/kimi"
)

func CoerceToolCall(schema Schema, call parse.ToolCall) parse.ToolCall {
	folded := foldToolName(call.Name)
	if name, ok := schema.Aliases()[folded]; ok {
		call.Name = name
	} else if name, ok := schema.Aliases()[call.Name]; ok {
		call.Name = name
	}
	call.Arguments = NormalizeToolCallArguments(call.Name, call.Arguments)
	return call
}

// NormalizeToolCallArguments rewrites Kimi/protobuf arg shapes to SapaLOQ executor fields (9router parity).
func NormalizeToolCallArguments(toolName string, args json.RawMessage) json.RawMessage {
	if len(args) == 0 {
		return json.RawMessage("{}")
	}
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil {
		return args
	}
	if inner, ok := obj["input"].(string); ok && strings.TrimSpace(inner) != "" {
		synthetic := fmt.Sprintf("<｜tool▁call▁begin｜>%s<|tool_sep|>%s<｜tool▁call▁end｜>", toolName, inner)
		if extracted := kimi.ExtractWithTokens(synthetic, kimi.DefaultTokens()); len(extracted.Calls) > 0 {
			if len(extracted.Calls[0].Arguments) > 0 {
				return extracted.Calls[0].Arguments
			}
		}
	}
	name := foldToolName(toolName)
	if name == "web_search" {
		if q, _ := obj["query"].(string); strings.TrimSpace(q) == "" {
			if st, ok := obj["search_term"].(string); ok && strings.TrimSpace(st) != "" {
				obj["query"] = strings.TrimSpace(st)
			}
		}
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return args
	}
	return b
}
