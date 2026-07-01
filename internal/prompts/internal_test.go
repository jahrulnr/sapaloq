package prompts

import (
	"strings"
	"testing"
)

func TestCatalogKeysResolve(t *testing.T) {
	for _, e := range Catalog() {
		switch e.Tier {
		case TierEditable:
			body := Default(e.Key)
			if strings.TrimSpace(body) == "" {
				t.Fatalf("editable key %q (%s) resolved empty", e.Key, e.File)
			}
		case TierInternal:
			body := GetInternal(e.Key)
			if strings.TrimSpace(body) == "" {
				t.Fatalf("internal key %q (%s) resolved empty", e.Key, e.File)
			}
		case TierBridge:
			// Reference only; prose lives in bridge package.
		}
	}
}

func TestRenderInternalClarificationMediator(t *testing.T) {
	got, err := RenderInternal(KeyClarificationMediator, ClarificationMediatorData{TaskID: "task-123"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `task_id="task-123"`) {
		t.Fatalf("mediator render missing task_id: %q", got)
	}
}

func TestRenderInternalRuntimeContext(t *testing.T) {
	got, err := RenderInternal(KeyTemplateRuntimeContext, RuntimeContextData{
		Workspace:      "/tmp/ws",
		ConfigPath:     "/tmp/cfg",
		DataPath:       "/tmp/data",
		MemoryPath:     "/tmp/mem",
		StatePath:      "/tmp/state",
		PromptsPath:    "/tmp/prompts",
		SkillsPath:     "/tmp/skills",
		VaultPath:      "/tmp/vault",
		RunPath:        "/tmp/run",
		EtcPath:        "/tmp/etc",
		RuntimeRoadmap: "/tmp/etc/ROADMAP.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "workspace=/tmp/ws") {
		t.Fatalf("runtime context missing workspace: %q", got)
	}
}

func TestValidateInternal(t *testing.T) {
	if err := ValidateInternal(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeContextFallbackMatchesTemplate(t *testing.T) {
	data := RuntimeContextData{
		Workspace:      "/ws",
		ConfigPath:     "/cfg",
		DataPath:       "/data",
		MemoryPath:     "/mem",
		StatePath:      "/state",
		PromptsPath:    "/prompts",
		SkillsPath:     "/skills",
		VaultPath:      "/vault",
		RunPath:        "/run",
		EtcPath:        "/etc",
		RuntimeRoadmap: "/etc/ROADMAP.md",
	}
	got, err := RenderInternal(KeyTemplateRuntimeContext, data)
	if err != nil {
		t.Fatal(err)
	}
	fallback := RuntimeContextFallback(data)
	if strings.TrimSpace(got) != strings.TrimSpace(fallback) {
		t.Fatalf("template/fallback drift:\ntemplate=%q\nfallback=%q", got, fallback)
	}
}

func TestComposeRoleOrchestrator(t *testing.T) {
	got := ComposeRole(nil, RoleOrchestrator)
	if !strings.Contains(got, "---") {
		t.Fatalf("compose orchestrator should join layers with ---: %q", got[:min(80, len(got))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
