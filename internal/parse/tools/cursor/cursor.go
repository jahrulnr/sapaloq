package cursor

import (
	"encoding/json"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

type ClientSideToolV2Call struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func ParseClientSideToolV2Call(frame []byte) (parse.ToolCall, bool) {
	var call ClientSideToolV2Call
	if err := json.Unmarshal(frame, &call); err != nil {
		return parse.ToolCall{}, false
	}
	if strings.TrimSpace(call.Name) == "" {
		return parse.ToolCall{}, false
	}
	return parse.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments, Source: "cursor_proto"}, true
}
