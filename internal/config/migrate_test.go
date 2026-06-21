package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.1.0", -1},
		{"1.1.0", "1.0.0", 1},
		{"1.1.0", "1.1.0", 0},
		{"1.1", "1.1.0", 0},
		{"2.0.0", "1.9.9", 1},
		{"v1.2.0", "1.2.0", 0},
		{"1.2.0-rc1", "1.2.0", 0},
	}
	for _, c := range cases {
		got, err := compareSemver(c.a, c.b)
		if err != nil {
			t.Fatalf("compareSemver(%q,%q) error: %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Fatalf("compareSemver(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
	if _, err := compareSemver("abc", "1.0.0"); err == nil {
		t.Fatalf("expected error for non-numeric version")
	}
}

func TestMigrateLowerVersionUpgrades(t *testing.T) {
	raw := map[string]any{"schemaVersion": "1.0.0"}
	out, changed, err := migrateRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatalf("expected migration to change a 1.0.0 config")
	}
	if out["schemaVersion"] != CurrentSchemaVersion {
		t.Fatalf("expected schemaVersion bumped to %s, got %v", CurrentSchemaVersion, out["schemaVersion"])
	}
}

func TestMigrateEqualVersionNoChange(t *testing.T) {
	raw := map[string]any{"schemaVersion": CurrentSchemaVersion}
	_, changed, err := migrateRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("expected no change for current-version config")
	}
}

func TestMigrateHigherVersionLeftAsIs(t *testing.T) {
	raw := map[string]any{"schemaVersion": "9.9.9", "custom": "keep"}
	out, changed, err := migrateRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("expected newer config to be left as-is")
	}
	if out["custom"] != "keep" {
		t.Fatalf("forward-compat must preserve unknown fields")
	}
}

func TestMigrateMissingVersionTreatedAsBaseline(t *testing.T) {
	raw := map[string]any{}
	out, changed, err := migrateRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || out["schemaVersion"] != CurrentSchemaVersion {
		t.Fatalf("blank version should upgrade to current; changed=%v ver=%v", changed, out["schemaVersion"])
	}
}

func TestLoadMigratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// Minimal but VALID 1.0.0 config (mandatory llmBridge present).
	old := map[string]any{
		"schemaVersion": "1.0.0",
		"llmBridge": map[string]any{
			"providerKey": "cursor",
			"providers": []any{
				map[string]any{
					"key": "cursor", "driver": "cursor-bridge",
					"endpoint": "https://x", "model": "m", "credentialsEnv": "E",
				},
			},
		},
	}
	b, _ := json.MarshalIndent(old, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("loaded config version = %s, want %s", cfg.SchemaVersion, CurrentSchemaVersion)
	}
	// Prompts block should be defaulted-in (enabled) by WithDefaults.
	if !cfg.Prompts.Enabled {
		t.Fatalf("expected prompts enabled by default after migration")
	}
	// The on-disk file should have been rewritten with the bumped version.
	raw, _ := os.ReadFile(path)
	var persisted map[string]any
	_ = json.Unmarshal(raw, &persisted)
	if persisted["schemaVersion"] != CurrentSchemaVersion {
		t.Fatalf("expected persisted schemaVersion %s, got %v", CurrentSchemaVersion, persisted["schemaVersion"])
	}
}

func TestMigrate110AlignsActiveConfigNames(t *testing.T) {
	raw := map[string]any{
		"schemaVersion": "1.1.0",
		"skills": map[string]any{
			"directory":        "/tmp/skills",
			"indexOnBoot":      true,
			"allowAgentCreate": true,
		},
		"prompts": map[string]any{
			"rolesPath":               "/tmp/roles",
			"rolesOverlayPath":        "/tmp/roles.d",
			"assembleOnSpawn":         true,
			"maxRolePromptTokens":     2500,
			"includeOverlayByDefault": true,
		},
		"events": map[string]any{
			"busPath": "/tmp/events.jsonl",
			"bus":     map[string]any{"socketPath": "/tmp/s.sock"},
		},
	}
	out, changed, err := migrateRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || out["schemaVersion"] != CurrentSchemaVersion {
		t.Fatalf("migration did not reach current version: %#v", out)
	}
	skills := out["skills"].(map[string]any)
	if skills["dir"] != "/tmp/skills" {
		t.Fatalf("skills.dir not migrated: %#v", skills)
	}
	if _, exists := skills["directory"]; exists {
		t.Fatalf("deprecated skills.directory retained: %#v", skills)
	}
	prompts := out["prompts"].(map[string]any)
	if prompts["dir"] != "/tmp/roles" || prompts["enabled"] != true {
		t.Fatalf("prompts not migrated: %#v", prompts)
	}
	events := out["events"].(map[string]any)
	bus := events["bus"].(map[string]any)
	if bus["walPath"] != "/tmp/events.jsonl" {
		t.Fatalf("events WAL not migrated: %#v", events)
	}
}
