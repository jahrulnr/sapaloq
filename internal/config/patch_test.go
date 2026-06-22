package config

import (
	"strings"
	"testing"
)

func TestApplyPatchValidatesNestedLeafPaths(t *testing.T) {
	raw := map[string]any{
		"orchestrator": map[string]any{
			"completion": map[string]any{"notifyUserOnDone": false},
		},
	}
	allowed := []string{"/orchestrator/completion"}
	if err := ApplyPatch(raw, map[string]any{
		"orchestrator": map[string]any{
			"completion": map[string]any{"notifyUserOnDone": true},
		},
	}, allowed); err != nil {
		t.Fatalf("active nested patch rejected: %v", err)
	}
	completion := raw["orchestrator"].(map[string]any)["completion"].(map[string]any)
	if completion["notifyUserOnDone"] != true {
		t.Fatalf("patch not applied: %#v", raw)
	}

	err := ApplyPatch(raw, map[string]any{
		"orchestrator": map[string]any{
			"spawnRouting": map[string]any{"autoApprovePlan": true},
		},
	}, allowed)
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("unsupported patch should fail clearly, got %v", err)
	}
}

func TestValidateRawRejectsInvalidCandidateBeforeSave(t *testing.T) {
	raw := map[string]any{
		"llmBridge": map[string]any{
			"providerKey": "missing",
			"providers": []any{
				map[string]any{
					"key": "cursor", "driver": "cursor-bridge",
					"endpoint": "https://x", "model": "m", "credentialsEnv": "TOKEN",
				},
			},
		},
	}
	if _, err := ValidateRaw(raw); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("invalid candidate should be rejected, got %v", err)
	}
}
