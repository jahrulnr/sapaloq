package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"sync"
	"text/template"
)

//go:embed all:internal
var internalFS embed.FS

var (
	internalCache   map[string]string
	internalCacheMu sync.RWMutex
)

func init() {
	internalCache = map[string]string{}
}

// GetInternal returns the ship-only internal prompt for key. Unknown keys return "".
func GetInternal(key string) string {
	if body, ok := loadInternal(key); ok {
		return body
	}
	return ""
}

// RenderInternal executes a text/template on the internal prompt for key.
func RenderInternal(key string, data any) (string, error) {
	raw, ok := loadInternalRaw(key)
	if !ok {
		return "", fmt.Errorf("prompts: unknown internal key %q", key)
	}
	if data == nil {
		return strings.TrimSpace(raw), nil
	}
	tmpl, err := template.New(key).Parse(raw)
	if err != nil {
		return "", fmt.Errorf("prompts: parse %q: %w", key, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prompts: render %q: %w", key, err)
	}
	return buf.String(), nil
}

// RuntimeContextData carries template fields for runtime-context.md.
type RuntimeContextData struct {
	Workspace       string
	ConfigPath      string
	DataPath        string
	MemoryPath      string
	StatePath       string
	PromptsPath     string
	SkillsPath      string
	VaultPath       string
	RunPath         string
	EtcPath         string
	RuntimeRoadmap  string
}

// ResumeNudgeData carries template fields for resume nudge fragments.
type ResumeNudgeData struct {
	PriorStatus string
	PriorError  string
}

// ClarificationMediatorData carries template fields for clarification mediator.
type ClarificationMediatorData struct {
	TaskID string
}

func loadInternal(key string) (string, bool) {
	internalCacheMu.RLock()
	if v, ok := internalCache[key]; ok {
		internalCacheMu.RUnlock()
		return v, v != ""
	}
	internalCacheMu.RUnlock()

	body, ok := loadInternalRaw(key)
	if !ok {
		return "", false
	}
	body = strings.TrimRight(body, "\n")
	if body != "" && !strings.HasSuffix(body, "\n") && strings.Contains(body, "\n") {
		// Preserve trailing newline for single-line fragments used with WriteString.
	}
	internalCacheMu.Lock()
	internalCache[key] = body
	internalCacheMu.Unlock()
	return body, body != ""
}

func loadInternalRaw(key string) (string, bool) {
	file, ok := internalKeyFile[key]
	if !ok {
		return "", false
	}
	b, err := internalFS.ReadFile(file)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// Resolve shows the active content for a catalog key. For editable roles, mgr
// (may be nil) supplies on-disk override; internal keys always use embed.
func Resolve(mgr *Manager, key string) string {
	if file, ok := internalKeyFile[key]; ok {
		if body, ok := loadInternalRaw(key); ok {
			return body
		}
		_ = file
		return ""
	}
	if _, ok := fileFor(key); ok {
		if mgr != nil {
			if v := mgr.Get(key); v != "" {
				return v
			}
		}
		return Default(key)
	}
	return ""
}

// ComposeRole builds persona → rules → role for preview/CLI (mirrors orchestrator).
func ComposeRole(mgr *Manager, role string) string {
	if role == RolePersona || role == RoleRules {
		return strings.TrimSpace(Resolve(mgr, role))
	}
	parts := make([]string, 0, 3)
	if persona := strings.TrimSpace(Resolve(mgr, RolePersona)); persona != "" {
		parts = append(parts, persona)
	}
	if rules := strings.TrimSpace(Resolve(mgr, RoleRules)); rules != "" {
		parts = append(parts, rules)
	}
	if base := strings.TrimSpace(Resolve(mgr, role)); base != "" {
		parts = append(parts, base)
	}
	return strings.Join(parts, "\n---\n")
}

// RuntimeContextFallback renders the runtime block without text/template when
// RenderInternal fails (centered-prompt migration must not silently drop paths).
func RuntimeContextFallback(data RuntimeContextData) string {
	return fmt.Sprintf(`---
# SapaLOQ runtime variables

workspace=%s
config_path=%s
data_path=%s
memory_path=%s
state_path=%s
prompts_path=%s
skills_path=%s
vault_path=%s
run_path=%s
etc_path=%s
runtime_roadmap=%s

Authoritative tool cwd is workspace= above. Relative paths and default exec cwd resolve from it; absolute paths are used as given.`,
		data.Workspace,
		data.ConfigPath,
		data.DataPath,
		data.MemoryPath,
		data.StatePath,
		data.PromptsPath,
		data.SkillsPath,
		data.VaultPath,
		data.RunPath,
		data.EtcPath,
		data.RuntimeRoadmap)
}

// ValidateInternal checks every ship-only internal prompt resolves non-empty.
func ValidateInternal() error {
	for _, e := range Catalog() {
		if e.Tier != TierInternal {
			continue
		}
		if strings.TrimSpace(GetInternal(e.Key)) == "" {
			return fmt.Errorf("prompts: internal key %q is empty", e.Key)
		}
	}
	return nil
}

// PreviewBlocks lists internal keys typically injected for a role stack (static only).
func PreviewBlocks(role string) []string {
	_ = role
	return []string{
		KeyTemplateRuntimeContext,
		KeyBlockNegativeGuidanceHeader,
		KeyBlockPrefetchHeader,
		KeyBlockSkillsHeader,
		KeyBlockActorEventsHeader,
		KeyBlockActorEventsFooter,
	}
}
