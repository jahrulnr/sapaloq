package provider

import (
	"encoding/json"
	"sync"
)

// Tool schemas let the upstream model know the exact parameters of each
// declared tool. Without a schema the bridge falls back to an open object
// (`additionalProperties: true`), which works but gives the model no argument
// hints. The orchestrator registers concrete schemas at init so assessment /
// workspace tools advertise their parameters.

var (
	toolSchemaMu       sync.RWMutex
	toolSchemaRegistry = map[string]json.RawMessage{}
)

// openSchema is the permissive fallback for tools without a registered schema.
var openSchema = json.RawMessage(`{"type":"object","additionalProperties":true}`)

// RegisterToolSchema records the JSON-schema parameter object for a tool name.
// Safe to call from multiple packages during init.
func RegisterToolSchema(name string, schema json.RawMessage) {
	if name == "" || len(schema) == 0 {
		return
	}
	toolSchemaMu.Lock()
	toolSchemaRegistry[name] = schema
	toolSchemaMu.Unlock()
}

// toolSchemaFor returns the registered schema for a tool, or the open fallback.
func toolSchemaFor(name string) json.RawMessage {
	toolSchemaMu.RLock()
	schema, ok := toolSchemaRegistry[name]
	toolSchemaMu.RUnlock()
	if ok && len(schema) > 0 {
		return schema
	}
	return openSchema
}
