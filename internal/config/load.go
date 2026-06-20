package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
)

type Config struct {
	SchemaVersion string             `json:"schemaVersion"`
	Runtime       RuntimeConfig      `json:"runtime"`
	LLMBridge     LLMBridgeRoot      `json:"llmBridge"`
	Commands      CommandsConfig     `json:"commands"`
	Events        EventsConfig       `json:"events"`
	Orchestrator  OrchestratorConfig `json:"orchestrator"`
	SubAgents     SubAgentsConfig    `json:"subAgents,omitempty"`
	Feedback      FeedbackConfig     `json:"feedback,omitempty"`
	Storage       StorageConfig      `json:"storage,omitempty"`
	Skills        SkillsConfig       `json:"skills,omitempty"`
	Platform      PlatformConfig     `json:"platform,omitempty"`
	Nodes         NodesConfig        `json:"nodes,omitempty"`
	Prompts       PromptsConfig      `json:"prompts,omitempty"`
}

// PromptsConfig governs the file-driven, replaceable per-mode system prompts
// (Ask, planner, agent, scribe). Defaults ship embedded in the binary and are
// materialized to Dir; a user edit is always preserved, while an unmodified
// file is transparently upgraded when the shipped default changes.
type PromptsConfig struct {
	// Enabled toggles materializing/loading on-disk prompts. Because the JSON
	// zero value of a bool is false, an entirely-absent prompts block is
	// treated as enabled (see WithDefaults). An explicit {"enabled": false}
	// keeps the prompts inert (embedded defaults are still served in-memory).
	Enabled bool `json:"enabled"`
	// Dir is the prompts directory (supports ~). Default ~/.config/sapaloq/prompts.
	Dir string `json:"dir,omitempty"`
}

// WithDefaults fills sane prompt defaults. A fully-unset block (enabled=false,
// no dir) is treated as enabled with the default dir — mirroring the skills
// config convention so an older config without a prompts block still gets the
// feature.
func (p PromptsConfig) WithDefaults() PromptsConfig {
	if !p.Enabled && strings.TrimSpace(p.Dir) == "" {
		p.Enabled = true
	}
	if strings.TrimSpace(p.Dir) == "" {
		p.Dir = "~/.config/sapaloq/prompts"
	}
	return p
}

// NodesConfig governs remote sub-agent nodes. Remote nodes only ever receive a
// bounded context packet (never the memory bus); these knobs add policy on top.
type NodesConfig struct {
	// AllowRemoteRoles lists roles permitted to run on remote nodes. Empty =
	// none (local-only) unless a role's node is explicitly enabled.
	AllowRemoteRoles []string `json:"allowRemoteRoles,omitempty"`
	// RequireTls rejects ws:// (non-TLS) remote endpoints when true. (JSON key
	// matches the existing config.example.json: requireTlsRemote.)
	RequireTls bool `json:"requireTlsRemote,omitempty"`
	// AllowSharedMemoryRemote permits share_memory=1 on remote nodes (off by
	// default; remote always gets a bounded packet otherwise).
	AllowSharedMemoryRemote bool `json:"allowSharedMemoryRemote,omitempty"`
	// FallbackToLocalOnRemoteFail routes a failed remote spawn to local-default.
	FallbackToLocalOnRemoteFail bool `json:"fallbackToLocalOnRemoteFail,omitempty"`
}

// WithDefaults fills sane node policy defaults: TLS required, fallback on.
func (n NodesConfig) WithDefaults() NodesConfig {
	// A fully-unset block (no roles, all flags false) is treated as the safe
	// default: TLS required + fallback to local on failure.
	if len(n.AllowRemoteRoles) == 0 && !n.RequireTls && !n.AllowSharedMemoryRemote && !n.FallbackToLocalOnRemoteFail {
		n.RequireTls = true
		n.FallbackToLocalOnRemoteFail = true
	}
	return n
}

// PlatformConfig selects and tunes the desktop adapter (notifications, DND, …).
// adapter "auto" detects from the environment using detectOrder; an explicit
// adapter id forces that backend. When allowFallback is true, a failed/absent
// backend falls back to headless instead of erroring.
type PlatformConfig struct {
	Adapter       string   `json:"adapter,omitempty"`
	DetectOrder   []string `json:"detectOrder,omitempty"`
	AllowFallback bool     `json:"allowFallback,omitempty"`
}

// WithDefaults fills sane platform defaults: auto-detect, gnome→freedesktop→
// headless order, fallback allowed.
func (p PlatformConfig) WithDefaults() PlatformConfig {
	if strings.TrimSpace(p.Adapter) == "" {
		p.Adapter = "auto"
	}
	if len(p.DetectOrder) == 0 {
		p.DetectOrder = []string{"gnome", "freedesktop", "headless"}
	}
	// allowFallback defaults to true; a fully-unset struct (adapter empty) is
	// treated as fallback-on. An explicit {"allowFallback": false} is honored
	// only when the user also set an adapter (so it isn't masked by the
	// auto-default path). For simplicity we default it on whenever auto.
	if p.Adapter == "auto" {
		p.AllowFallback = true
	}
	return p
}

// SkillsConfig controls the file-driven skills system: where skill Markdown
// files live and how much skill guidance is injected per Ask turn. Skills are
// read-only context (no tool grants, no execution).
type SkillsConfig struct {
	// Enabled toggles the whole feature. Because the JSON zero value of a bool
	// is false, callers treat an entirely-absent skills block as enabled — see
	// WithDefaults / the absentEnabled handling at the call site.
	Enabled bool `json:"enabled"`
	// Dir is the skills directory (supports ~). Default ~/.config/sapaloq/skills.
	Dir string `json:"dir,omitempty"`
	// MaxLoadPerTurn bounds how many skills are injected per turn. Default 2.
	MaxLoadPerTurn int `json:"maxLoadPerTurn,omitempty"`
	// MaxBodyLines bounds each injected skill body. Default 40.
	MaxBodyLines int `json:"maxBodyLines,omitempty"`
}

// WithDefaults fills sane skill defaults and clamps bounds. A fully-unset block
// (no dir, no limits, enabled=false) is treated as enabled — mirroring the
// feedback config convention — so an older config without a skills block still
// gets the feature. An explicit {"enabled": false} disables it.
func (s SkillsConfig) WithDefaults() SkillsConfig {
	if !s.Enabled && strings.TrimSpace(s.Dir) == "" && s.MaxLoadPerTurn == 0 && s.MaxBodyLines == 0 {
		s.Enabled = true
	}
	if strings.TrimSpace(s.Dir) == "" {
		s.Dir = "~/.config/sapaloq/skills"
	}
	if s.MaxLoadPerTurn <= 0 {
		s.MaxLoadPerTurn = 2
	}
	if s.MaxLoadPerTurn > 5 {
		s.MaxLoadPerTurn = 5
	}
	if s.MaxBodyLines <= 0 {
		s.MaxBodyLines = 40
	}
	if s.MaxBodyLines < 5 {
		s.MaxBodyLines = 5
	}
	if s.MaxBodyLines > 200 {
		s.MaxBodyLines = 200
	}
	return s
}

// StorageConfig maps note "intents" and boundary paths the scribe sub-agent may
// write to. Writes are restricted to these declared paths (boundary-enforced).
type StorageConfig struct {
	Paths   []StoragePath     `json:"paths,omitempty"`
	Intents map[string]string `json:"intents,omitempty"`
}

// StoragePath is one named, mode-scoped destination file (e.g. personal notes).
type StoragePath struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Mode        string `json:"mode,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description,omitempty"`
}

// Resolve picks a storage path by intent phrase, explicit id, or mode/kind.
// Preference order: explicit id → intent phrase → mode (+optional kind) → "".
func (s StorageConfig) Resolve(id, intent, mode, kind string) (StoragePath, bool) {
	if id != "" {
		if p, ok := s.byID(id); ok {
			return p, true
		}
	}
	if intent != "" {
		if mapped, ok := s.Intents[strings.ToLower(strings.TrimSpace(intent))]; ok {
			if p, ok := s.byID(mapped); ok {
				return p, true
			}
		}
	}
	if mode != "" {
		for _, p := range s.Paths {
			if !strings.EqualFold(p.Mode, mode) {
				continue
			}
			if kind == "" || strings.EqualFold(p.Kind, kind) {
				return p, true
			}
		}
	}
	return StoragePath{}, false
}

func (s StorageConfig) byID(id string) (StoragePath, bool) {
	for _, p := range s.Paths {
		if p.ID == id {
			return p, true
		}
	}
	return StoragePath{}, false
}

// FeedbackConfig controls the explicit 👍/👎 reward loop and how much negative
// guidance (do_not_repeat) is injected into the Ask prompt each turn.
type FeedbackConfig struct {
	// ExplicitSignalsEnabled toggles the whole feedback feature. Defaults to
	// true (zero value would disable it, so consumers treat the unset config
	// via FeedbackWithDefaults).
	ExplicitSignalsEnabled bool `json:"explicitSignalsEnabled"`
	// MaxNegativeSlicesPerTurn bounds how many do_not_repeat facts are injected
	// into the Ask system prompt per turn. Defaults to 1.
	MaxNegativeSlicesPerTurn int `json:"maxNegativeSlicesPerTurn"`
}

// FeedbackWithDefaults fills sane defaults: feedback enabled, 1 negative slice.
// Because the JSON zero value of a bool is false, callers should treat an
// entirely-absent feedback block as "enabled" — handled here by only enabling
// defaults when the struct looks unset.
func (f FeedbackConfig) WithDefaults() FeedbackConfig {
	if f.MaxNegativeSlicesPerTurn <= 0 {
		f.MaxNegativeSlicesPerTurn = 1
	}
	if f.MaxNegativeSlicesPerTurn > 10 {
		f.MaxNegativeSlicesPerTurn = 10
	}
	return f
}

// SubAgentsConfig models the per-role sub-agent settings from config.json.
// Only the fields the orchestrator actually consumes are typed here (e.g.
// MaxTurns); the rest of each role's JSON is preserved loosely so future
// settings can be added without breaking parsing.
type SubAgentsConfig struct {
	Roles map[string]SubAgentRole `json:"roles,omitempty"`
}

// SubAgentRole captures the consumable knobs for one sub-agent role.
type SubAgentRole struct {
	Description   string   `json:"description,omitempty"`
	AllowedTools  []string `json:"allowedTools,omitempty"`
	ToolPolicy    string   `json:"toolPolicy,omitempty"`
	MaxTurns      int      `json:"maxTurns,omitempty"`
	CanEditConfig bool     `json:"canEditConfig,omitempty"`
}

type RuntimeConfig struct {
	DataDir    string `json:"dataDir"`
	BinaryName string `json:"binaryName"`
}

// LLMBridge is one provider entry — the smallest unit of bridge configuration.
// Each entry is self-contained: which driver, which endpoint, which
// credentials, and (for provider-bridge entries) which wire format + auth
// scheme + API version. Key is required when the entry is part of a
// providers array; it is unused at the top level.
type LLMBridge struct {
	Key            string   `json:"key,omitempty"`
	Driver         string   `json:"driver"`
	Endpoint       string   `json:"endpoint"`
	Model          string   `json:"model"`
	CredentialsEnv string   `json:"credentialsEnv"`
	DeclaredTools  []string `json:"declaredTools,omitempty"`
	// Parser selects the request/response wire format for provider-bridge.
	// Recognized values: "openai", "claude", "kimi". Auto-detected from
	// Model and Endpoint when empty.
	Parser string `json:"parser,omitempty"`
	// AuthScheme picks the credential header layout. "bearer" sends
	// `Authorization: Bearer <token>` (OpenAI / Kimi / OpenRouter default).
	// "x-api-key" sends `x-api-key: <token>` (Anthropic). Auto-derived from
	// Parser when empty.
	AuthScheme string `json:"authScheme,omitempty"`
	// APIVersion is sent as `anthropic-version` for the claude parser. Defaults
	// to "2023-06-01" when empty.
	APIVersion string `json:"apiVersion,omitempty"`
	// ReasoningEffort controls thinking intensity. For openai parser it maps
	// to the `reasoning_effort` parameter (low|medium|high). For claude
	// parser it maps to `thinking.budget_tokens` (low=1024, medium=5000,
	// high=16000). For kimi parser it toggles the `thinking.type` field
	// (set to "enabled" when non-empty, "disabled" when empty).
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// MaxTokens bounds the model output. Maps to `max_completion_tokens`
	// (openai/kimi) or `max_tokens` (claude).
	MaxTokens int `json:"maxTokens,omitempty"`
	// ContextWindow bounds the maximum input the bridge will forward to the
	// model in a single turn, in tokens. The bridge estimates tokens as
	// len(content)/4 and drops the oldest non-system messages when the
	// conversation exceeds this. Defaults to 1,000,000 (matches Claude
	// Sonnet 4, Gemini 2.5 Pro, GPT-5 family).
	ContextWindow int `json:"contextWindow,omitempty"`
}

// LLMBridgeRoot is the top-level llmBridge config block — registry of
// available providers plus bridge-management metadata that applies across
// all providers (coercion, fallback, community bridge).
type LLMBridgeRoot struct {
	// ProviderKey selects the active entry from Providers. Required.
	ProviderKey string `json:"providerKey"`
	// Providers is the registry of available provider entries. Required,
	// must contain at least one entry.
	Providers []LLMBridge `json:"providers"`
	// Coercion is the fake-tool sanitizer config (used by cursor-bridge).
	Coercion *CoercionConfig `json:"coercion,omitempty"`
	// Fallback is the fallback driver config used when the active driver
	// fails with a recognised error class.
	Fallback *FallbackConfig `json:"fallback,omitempty"`
	// CommunityBridge is an optional third-party bridge binary path
	// (not 9router; user/community supplied).
	CommunityBridge *CommunityBridgeConfig `json:"communityBridge,omitempty"`
}

// CoercionConfig is the fake-tool sanitizer config used by cursor-bridge.
type CoercionConfig struct {
	Enabled    bool   `json:"enabled"`
	SchemaPath string `json:"schemaPath,omitempty"`
}

// FallbackConfig describes the fallback driver and the error classes that
// trigger fallback.
type FallbackConfig struct {
	Driver string   `json:"driver"`
	On     []string `json:"on,omitempty"`
}

// CommunityBridgeConfig is the optional third-party bridge binary config.
type CommunityBridgeConfig struct {
	Enabled  bool   `json:"enabled"`
	Command  string `json:"command,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// Validate checks that the LLMBridgeRoot has a usable provider registry.
// Returns an error if providerKey is empty, providers array is empty,
// any entry has empty Key, duplicate Keys exist, or providerKey doesn't
// match any entry.
func (r LLMBridgeRoot) Validate() error {
	if r.ProviderKey == "" {
		return fmt.Errorf("llmBridge.providerKey is required")
	}
	if len(r.Providers) == 0 {
		return fmt.Errorf("llmBridge.providers must contain at least one entry")
	}
	seen := make(map[string]int)
	for i, p := range r.Providers {
		if p.Key == "" {
			return fmt.Errorf("llmBridge.providers[%d]: key is required", i)
		}
		if _, dup := seen[p.Key]; dup {
			return fmt.Errorf("llmBridge.providers[%d]: duplicate key %q", i, p.Key)
		}
		seen[p.Key] = i
	}
	if _, ok := seen[r.ProviderKey]; !ok {
		return fmt.Errorf("llmBridge.providerKey %q does not match any entry", r.ProviderKey)
	}
	return nil
}

// ActiveProvider returns the provider entry selected by ProviderKey.
// It does not perform Validate first — callers should Validate during
// config load.
func (r LLMBridgeRoot) ActiveProvider() (LLMBridge, error) {
	for _, p := range r.Providers {
		if p.Key == r.ProviderKey {
			return p, nil
		}
	}
	return LLMBridge{}, fmt.Errorf("llmBridge: providerKey %q not found in providers array", r.ProviderKey)
}

type EventsConfig struct {
	Bus BusConfig `json:"bus"`
}

type BusConfig struct {
	SocketPath        string `json:"socketPath"`
	WALPath           string `json:"walPath,omitempty"`
	WatcherBufferSize int    `json:"watcherBufferSize,omitempty"`
	ReplayOnBoot      bool   `json:"replayOnBoot,omitempty"`
}

type OrchestratorConfig struct {
	Continuation ContinuationConfig `json:"continuation"`
	Compaction   CompactionConfig   `json:"compaction"`
}

type ContinuationConfig struct {
	MaxInferenceTurns     int `json:"maxInferenceTurns"`
	MaxToolCalls          int `json:"maxToolCalls"`
	MaxWallTimeMinutes    int `json:"maxWallTimeMinutes"`
	MaxNoProgressTurns    int `json:"maxNoProgressTurns"`
	MaxIdenticalToolCalls int `json:"maxIdenticalToolCalls"`
	MaxWaitSeconds        int `json:"maxWaitSeconds"`
}

type CompactionConfig struct {
	BackgroundThreshold    float64 `json:"backgroundThreshold"`
	BlockingThreshold      float64 `json:"blockingThreshold"`
	PreserveRecentFraction float64 `json:"preserveRecentFraction"`
	ResumeAfterCompaction  bool    `json:"resumeAfterCompaction"`
}

func DefaultOrchestratorConfig() OrchestratorConfig {
	return OrchestratorConfig{
		Continuation: ContinuationConfig{
			MaxInferenceTurns:     128,
			MaxToolCalls:          512,
			MaxWallTimeMinutes:    30,
			MaxNoProgressTurns:    5,
			MaxIdenticalToolCalls: 5,
			MaxWaitSeconds:        120,
		},
		Compaction: CompactionConfig{
			BackgroundThreshold:    0.70,
			BlockingThreshold:      0.88,
			PreserveRecentFraction: 0.30,
			ResumeAfterCompaction:  true,
		},
	}
}

func (c OrchestratorConfig) WithDefaults() OrchestratorConfig {
	defaults := DefaultOrchestratorConfig()
	if c.Continuation.MaxInferenceTurns <= 0 {
		c.Continuation.MaxInferenceTurns = defaults.Continuation.MaxInferenceTurns
	}
	if c.Continuation.MaxToolCalls <= 0 {
		c.Continuation.MaxToolCalls = defaults.Continuation.MaxToolCalls
	}
	if c.Continuation.MaxWallTimeMinutes <= 0 {
		c.Continuation.MaxWallTimeMinutes = defaults.Continuation.MaxWallTimeMinutes
	}
	if c.Continuation.MaxNoProgressTurns <= 0 {
		c.Continuation.MaxNoProgressTurns = defaults.Continuation.MaxNoProgressTurns
	}
	if c.Continuation.MaxIdenticalToolCalls <= 0 {
		c.Continuation.MaxIdenticalToolCalls = defaults.Continuation.MaxIdenticalToolCalls
	}
	if c.Continuation.MaxWaitSeconds <= 0 {
		c.Continuation.MaxWaitSeconds = defaults.Continuation.MaxWaitSeconds
	}
	if c.Compaction.BackgroundThreshold <= 0 || c.Compaction.BackgroundThreshold >= 1 {
		c.Compaction.BackgroundThreshold = defaults.Compaction.BackgroundThreshold
	}
	if c.Compaction.BlockingThreshold <= 0 || c.Compaction.BlockingThreshold >= 1 {
		c.Compaction.BlockingThreshold = defaults.Compaction.BlockingThreshold
	}
	if c.Compaction.BlockingThreshold < c.Compaction.BackgroundThreshold {
		c.Compaction.BlockingThreshold = defaults.Compaction.BlockingThreshold
	}
	if c.Compaction.PreserveRecentFraction <= 0 || c.Compaction.PreserveRecentFraction >= 1 {
		c.Compaction.PreserveRecentFraction = defaults.Compaction.PreserveRecentFraction
	}
	return c
}

func DefaultConfig() Config {
	return Config{
		SchemaVersion: CurrentSchemaVersion,
		Runtime: RuntimeConfig{
			DataDir:    defaultDataDir,
			BinaryName: "sapaloq-core",
		},
		LLMBridge: LLMBridgeRoot{
			ProviderKey: "cursor",
			Providers: []LLMBridge{
				{
					Key:            "cursor",
					Driver:         "cursor-bridge",
					Endpoint:       "https://api2.cursor.sh",
					Model:          "default",
					CredentialsEnv: "SAPALOQ_CURSOR_TOKEN",
				},
			},
		},
		Commands:     DefaultCommands(),
		Events:       EventsConfig{Bus: BusConfig{SocketPath: "~/.config/sapaloq/run/sapaloq.sock"}},
		Orchestrator: DefaultOrchestratorConfig(),
	}
}

func Load(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		path = ConfigPath(os.Getenv("SAPALOQ_CONFIG"), cfg)
	}
	if err := ensureConfigFile(path); err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, err
	}
	// Schema migration: decode to a raw map first so older configs can be
	// upgraded in place before being bound to the current struct. Old JSON
	// formats are always preserved (the upgrade chain is additive/idempotent).
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return Config{}, err
	}
	migrated, changed, mErr := migrateRaw(raw)
	if mErr != nil {
		return Config{}, mErr
	}
	if changed {
		if upgraded, mErr := json.Marshal(migrated); mErr == nil {
			b = upgraded
			// Persist the upgraded config so the bump happens once. Best-effort:
			// a read-only config dir must not fail Load.
			_ = SaveRaw(path, migrated, "schema-migration")
		}
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	cfg.Runtime.DataDir = ExpandPath(defaultIfEmpty(cfg.Runtime.DataDir, defaultDataDir))
	cfg.Events.Bus.SocketPath = ExpandPath(defaultIfEmpty(cfg.Events.Bus.SocketPath, "~/.config/sapaloq/run/sapaloq.sock"))
	cfg.Commands = cfg.Commands.WithDefaults()
	cfg.Orchestrator = cfg.Orchestrator.WithDefaults()
	cfg.Skills = cfg.Skills.WithDefaults()
	cfg.Platform = cfg.Platform.WithDefaults()
	cfg.Nodes = cfg.Nodes.WithDefaults()
	cfg.Prompts = cfg.Prompts.WithDefaults()
	if err := cfg.Commands.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.LLMBridge.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ensureConfigFile(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	b, err := os.ReadFile(exampleConfigPath())
	if err != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func exampleConfigPath() string {
	candidates := []string{
		filepath.Join("config", "config.example.json"),
		filepath.Join("sapaloq", "config", "config.example.json"),
	}
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(base, "config", "config.example.json"),
			filepath.Join(base, "..", "config", "config.example.json"),
		)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join("config", "config.example.json")
}

func Doctor(cfg Config) (string, error) {
	if err := cfg.Commands.Validate(); err != nil {
		return "", err
	}
	if err := cfg.LLMBridge.Validate(); err != nil {
		return "", err
	}
	dirs := RuntimeDirs(cfg)
	if err := EnsureRuntimeDirs(dirs); err != nil {
		return "", err
	}
	entry, err := cfg.LLMBridge.ActiveProvider()
	if err != nil {
		return "", err
	}
	creds, err := credentials.Load(credentials.Options{TokenEnv: entry.CredentialsEnv})
	if err != nil {
		return "", err
	}
	credSource := creds.Source
	probe := filepath.Join(dirs.RunDir, ".sapaloq-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return "", fmt.Errorf("socket directory is not writable: %w", err)
	}
	_ = os.Remove(probe)
	return credSource, nil
}

func defaultIfEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
