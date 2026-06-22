package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicExampleMatchesSchemaShape(t *testing.T) {
	root := filepath.Join("..", "..")
	exampleRaw, err := os.ReadFile(filepath.Join(root, "config", "config.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	schemaRaw, err := os.ReadFile(filepath.Join(root, "schema", "config.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var example any
	if err := json.Unmarshal(exampleRaw, &example); err != nil {
		t.Fatalf("example JSON: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		t.Fatalf("schema JSON: %v", err)
	}
	if err := validateSchemaShape(example, schema, schema, "$"); err != nil {
		t.Fatal(err)
	}
}

func TestPublicExampleLoadsActiveRuntimeFields(t *testing.T) {
	path := filepath.Join("..", "..", "config", "config.example.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Platform.Adapter != "auto" || len(cfg.Platform.DetectOrder) == 0 {
		t.Fatalf("platform config not loaded: %+v", cfg.Platform)
	}
	if !cfg.Skills.Enabled || !strings.HasSuffix(cfg.Skills.Dir, "/.config/sapaloq/skills") {
		t.Fatalf("skills config not loaded: %+v", cfg.Skills)
	}
	if !cfg.Prompts.Enabled || !strings.HasSuffix(cfg.Prompts.Dir, "/.config/sapaloq/prompts") {
		t.Fatalf("prompts config not loaded: %+v", cfg.Prompts)
	}
	if cfg.Vault.MaxLogBytes != 5<<20 || cfg.Vault.KeepRotatedFiles != 3 {
		t.Fatalf("vault config not loaded: %+v", cfg.Vault)
	}
	if !strings.HasSuffix(cfg.Events.Bus.WALPath, "/.config/sapaloq/state/events.jsonl") {
		t.Fatalf("event WAL path not loaded: %+v", cfg.Events.Bus)
	}
	planner, ok := cfg.SubAgents.Roles["planner"]
	if !ok || !containsString(planner.AllowedTools, "exec") {
		t.Fatalf("planner exploration tools not loaded: %+v", planner)
	}
	scribe, ok := cfg.SubAgents.Roles["scribe"]
	if !ok || containsString(scribe.AllowedTools, "exec") {
		t.Fatalf("scribe policy not loaded: %+v", scribe)
	}
}

func validateSchemaShape(value any, node, root map[string]any, path string) error {
	if ref, _ := node["$ref"].(string); ref != "" {
		resolved, err := resolveLocalRef(root, ref)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		node = resolved
	}
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := node["properties"].(map[string]any)
		additional := node["additionalProperties"]
		for key, child := range typed {
			childSchema, ok := properties[key].(map[string]any)
			if !ok {
				switch extra := additional.(type) {
				case bool:
					if !extra {
						return fmt.Errorf("%s.%s is not declared by schema", path, key)
					}
					continue
				case map[string]any:
					childSchema = extra
				default:
					continue
				}
			}
			if err := validateSchemaShape(child, childSchema, root, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		items, _ := node["items"].(map[string]any)
		if items == nil {
			return nil
		}
		for i, child := range typed {
			if err := validateSchemaShape(child, items, root, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveLocalRef(root map[string]any, ref string) (map[string]any, error) {
	const prefix = "#/"
	if !strings.HasPrefix(ref, prefix) {
		return nil, fmt.Errorf("unsupported ref %q", ref)
	}
	var current any = root
	for _, segment := range strings.Split(strings.TrimPrefix(ref, prefix), "/") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid ref %q", ref)
		}
		current, ok = object[segment]
		if !ok {
			return nil, fmt.Errorf("unresolved ref %q", ref)
		}
	}
	resolved, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ref %q is not an object", ref)
	}
	return resolved, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
