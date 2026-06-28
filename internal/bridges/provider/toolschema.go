package provider

import (
	"encoding/json"
	"strings"
	"sync"
)

// Tool schemas let the upstream model know the exact parameters of each
// declared tool. Without a schema the bridge falls back to an open object
// (`additionalProperties: true`), which works but gives the model no argument
// hints. The orchestrator registers concrete schemas at init so assessment /
// workspace tools advertise their parameters.

type toolEntry struct {
	schema      json.RawMessage
	description string
}

var (
	toolSchemaMu    sync.RWMutex
	toolRegistry    = map[string]toolEntry{}
)

// openSchema is the permissive fallback for tools without a registered schema.
var openSchema = json.RawMessage(`{"type":"object","additionalProperties":true}`)

// RegisterToolSchema records the JSON-schema parameter object for a tool name.
// Safe to call from multiple packages during init. Prefer RegisterTool when a
// description is available.
func RegisterToolSchema(name string, schema json.RawMessage) {
	RegisterTool(name, schema, "")
}

// RegisterTool records schema + human-facing description for a tool name.
func RegisterTool(name string, schema json.RawMessage, description string) {
	if name == "" || len(schema) == 0 {
		return
	}
	toolSchemaMu.Lock()
	toolRegistry[name] = toolEntry{schema: schema, description: strings.TrimSpace(description)}
	toolSchemaMu.Unlock()
}

// toolSchemaFor returns the registered schema for a tool, or the open fallback.
func toolSchemaFor(name string) json.RawMessage {
	toolSchemaMu.RLock()
	entry, ok := toolRegistry[name]
	toolSchemaMu.RUnlock()
	if ok && len(entry.schema) > 0 {
		return entry.schema
	}
	return openSchema
}

// RegisteredToolDescription returns the wire description for a registered tool.
func RegisteredToolDescription(name string) string {
	return toolDescriptionFor(name)
}

// RegisteredToolSchema returns the registered JSON schema for a tool.
func RegisteredToolSchema(name string) json.RawMessage {
	return toolSchemaFor(name)
}

// toolDescriptionFor returns the registered description for a tool, or "".
func toolDescriptionFor(name string) string {
	toolSchemaMu.RLock()
	entry, ok := toolRegistry[name]
	toolSchemaMu.RUnlock()
	if ok {
		return entry.description
	}
	return ""
}
