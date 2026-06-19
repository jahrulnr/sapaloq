package cursor

import (
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func CoerceToolCall(schema Schema, call parse.ToolCall) parse.ToolCall {
	folded := foldToolName(call.Name)
	if name, ok := schema.Aliases()[folded]; ok {
		call.Name = name
	} else if name, ok := schema.Aliases()[call.Name]; ok {
		call.Name = name
	}
	return call
}
