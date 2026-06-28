package cursor

import (
	"embed"
	"encoding/json"
)

//go:embed cursor-bridge.schema.json
var schemaFS embed.FS

type Schema struct {
	SchemaVersion string        `json:"schemaVersion"`
	Provider      ProviderBlock `json:"provider"`
}

type ProviderBlock struct {
	NativeTools            []string          `json:"nativeTools"`
	SessionNonTriggerTools []string          `json:"sessionNonTriggerTools"`
	Aliases                map[string]string `json:"aliases"`
	KimiTokens             []string          `json:"kimiTokens"`
	LeakPatterns           []string          `json:"leakPatterns"`
	GuardSafeReply         string            `json:"guardSafeReply"`
}

func (s Schema) Aliases() map[string]string {
	if s.Provider.Aliases == nil {
		return map[string]string{}
	}
	return s.Provider.Aliases
}

func (s Schema) KimiTokens() []string {
	return s.Provider.KimiTokens
}

func LoadSchema() (Schema, error) {
	b, err := schemaFS.ReadFile("cursor-bridge.schema.json")
	if err != nil {
		return Schema{}, err
	}
	var schema Schema
	if err := json.Unmarshal(b, &schema); err != nil {
		return Schema{}, err
	}
	if schema.Provider.Aliases == nil {
		schema.Provider.Aliases = map[string]string{}
	}
	return schema, nil
}
