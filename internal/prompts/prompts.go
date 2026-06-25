// Package prompts provides the default per-mode system prompts (Ask, planner,
// task-runner/agent, scribe) as Markdown that ships embedded in the binary but
// is materialized to disk so the user can edit it.
//
// "Updateable if non-modified": each shipped default is written to the prompts
// dir alongside a sha256 manifest recording the hash that was shipped. On a
// later boot, if the on-disk file still matches the shipped hash (user did not
// touch it) and the embedded default changed, the file is transparently
// upgraded to the new default. If the user modified the file, their version is
// always kept - SapaLOQ never clobbers user edits.
package prompts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed defaults/*.md
var defaultFS embed.FS

// Role keys. These map 1:1 to sub-agent roles plus the Ask orchestrator.
// RolePersona and RoleRules are special: they are shared layers prepended to
// every other role's prompt (see Orchestrator.systemPrompt), not modes of
// their own. RolePersona carries the core character ("how to carry yourself");
// RoleRules carries project-grounding instructions ("read the repo's rule
// files first").
const (
	RoleAsk        = "ask"
	RolePlanner    = "planner"
	RoleAgent      = "agent" // task-runner
	RoleScribe     = "scribe"
	RolePersona    = "persona"
	RoleRules      = "rules"
	manifestName   = "prompts.manifest.json"
	roleTaskRunner = "task-runner"
)

// fileFor maps a role key to its on-disk / embedded markdown filename.
func fileFor(role string) (string, bool) {
	switch role {
	case RoleAsk:
		return "ask.md", true
	case RolePlanner:
		return "planner.md", true
	case RoleAgent, roleTaskRunner:
		return "agent.md", true
	case RoleScribe:
		return "scribe.md", true
	case RolePersona:
		return "persona.md", true
	case RoleRules:
		return "rules.md", true
	default:
		return "", false
	}
}

// Manager loads and serves system prompts, preferring the on-disk (editable)
// copy and falling back to the embedded default. A nil/zero Manager is safe:
// Get falls back to embedded defaults.
type Manager struct {
	mu      sync.RWMutex
	dir     string
	enabled bool
	cache   map[string]string // role → resolved prompt content
}

// New constructs a Manager rooted at dir. When enabled, Sync materializes the
// embedded defaults to disk (upgrading unmodified files) and loads the on-disk
// copies. When disabled, the Manager still serves embedded defaults via Get.
// Disk errors are non-fatal: New never fails (worst case it serves embedded
// defaults), so prompt loading can never break startup.
func New(dir string, enabled bool) *Manager {
	m := &Manager{dir: strings.TrimSpace(dir), enabled: enabled, cache: map[string]string{}}
	if enabled && m.dir != "" {
		_ = m.Sync()
	}
	return m
}

// roles is the canonical set of files the manager manages.
func roles() []struct{ role, file string } {
	return []struct{ role, file string }{
		{RoleAsk, "ask.md"},
		{RolePlanner, "planner.md"},
		{RoleAgent, "agent.md"},
		{RoleScribe, "scribe.md"},
		{RolePersona, "persona.md"},
		{RoleRules, "rules.md"},
	}
}

// Sync materializes embedded defaults to disk and refreshes the in-memory
// cache. For each role: if the file is absent, write the default + record its
// shipped hash; if present and unmodified (on-disk hash == recorded shipped
// hash) but the embedded default changed, upgrade it + update the manifest; if
// present and modified by the user, keep it untouched. The resolved content
// (on-disk if present, else embedded) is cached for Get.
func (m *Manager) Sync() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dir == "" {
		return nil
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	manifest := m.loadManifest()
	for _, r := range roles() {
		def, ok := embeddedDefault(r.file)
		if !ok {
			continue
		}
		defHash := hash(def)
		path := filepath.Join(m.dir, r.file)
		onDisk, readErr := os.ReadFile(path)
		switch {
		case os.IsNotExist(readErr):
			// First run for this file: seed the shipped default.
			if err := os.WriteFile(path, []byte(def), 0o644); err == nil {
				manifest[r.file] = defHash
			}
			m.cache[r.role] = def
		case readErr != nil:
			// Unreadable on disk → serve embedded default this boot.
			m.cache[r.role] = def
		default:
			diskStr := string(onDisk)
			shipped := manifest[r.file]
			if shipped != "" && hash(diskStr) == shipped && shipped != defHash {
				// Unmodified by user AND default changed → upgrade in place.
				if err := os.WriteFile(path, []byte(def), 0o644); err == nil {
					manifest[r.file] = defHash
					m.cache[r.role] = def
					continue
				}
			}
			if shipped == "" {
				// No manifest record (e.g. pre-existing file): adopt current
				// disk content and record its hash so future upgrades only
				// apply when the user hasn't edited since.
				manifest[r.file] = hash(diskStr)
			}
			m.cache[r.role] = diskStr
		}
	}
	_ = m.saveManifest(manifest)
	return nil
}

// Get returns the system prompt for a role: the cached on-disk copy when loaded,
// otherwise the embedded default. Unknown roles return "".
func (m *Manager) Get(role string) string {
	if role == roleTaskRunner {
		role = RoleAgent
	}
	if m != nil {
		m.mu.RLock()
		if v, ok := m.cache[role]; ok && strings.TrimSpace(v) != "" {
			m.mu.RUnlock()
			return v
		}
		m.mu.RUnlock()
	}
	if file, ok := fileFor(role); ok {
		if def, ok := embeddedDefault(file); ok {
			return def
		}
	}
	return ""
}

// Persona returns the shared core-character prompt (persona.md): the on-disk
// copy when loaded, otherwise the embedded default. It is prepended to every
// role's prompt by Orchestrator.systemPrompt. A nil Manager still serves the
// embedded default.
func (m *Manager) Persona() string {
	return m.Get(RolePersona)
}

// Default returns the embedded (shipped) default for a role, ignoring any
// on-disk override. Useful for diffing/reset.
func Default(role string) string {
	if file, ok := fileFor(role); ok {
		if def, ok := embeddedDefault(file); ok {
			return def
		}
	}
	return ""
}

func embeddedDefault(file string) (string, bool) {
	b, err := defaultFS.ReadFile("defaults/" + file)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)) + "\n", true
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (m *Manager) manifestPath() string { return filepath.Join(m.dir, manifestName) }

// loadManifest reads the sha256 manifest (file → shipped-hash). A missing or
// corrupt manifest yields an empty map (treated as "no record").
func (m *Manager) loadManifest() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(m.manifestPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]string{}
	}
	return out
}

func (m *Manager) saveManifest(man map[string]string) error {
	b, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.manifestPath(), append(b, '\n'), 0o644)
}
