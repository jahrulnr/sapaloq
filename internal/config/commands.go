package config

import (
	"fmt"
	"regexp"
	"strings"
)

const SlashBoundaryPattern = `(^/|\s/|\n/)`

type CommandsConfig struct {
	TriggerChars []string       `json:"triggerChars"`
	Registry     []CommandEntry `json:"registry"`
}

type CommandEntry struct {
	ID          string `json:"id"`
	Prefix      string `json:"prefix"`
	Pattern     string `json:"pattern"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Enabled     bool   `json:"enabled"`
}

func DefaultCommands() CommandsConfig {
	return CommandsConfig{
		TriggerChars: []string{"/"},
		Registry: []CommandEntry{
			{
				ID:          "settings",
				Prefix:      "/settings",
				Pattern:     `/settings(?:\s+|$)`,
				Label:       "Settings",
				Description: "Patch config.json",
				Category:    "commands",
				Enabled:     true,
			},
			{
				ID:          "compaction",
				Prefix:      "/compaction",
				Pattern:     `/compaction(?:\s+|$)`,
				Label:       "Compaction",
				Description: "Compact active chat context",
				Category:    "commands",
				Enabled:     true,
			},
			{
				ID:          "reset",
				Prefix:      "/reset",
				Pattern:     `/(reset|clear)(?:\s+|$)`,
				Label:       "Reset",
				Description: "Start a fresh active chat session",
				Category:    "commands",
				Enabled:     true,
			},
			{
				ID:          "model",
				Prefix:      "/model",
				Pattern:     `/model(?:\s+|$)`,
				Label:       "Model",
				Description: "Switch active provider: /model <key>",
				Category:    "commands",
				Enabled:     true,
			},
			{
				ID:          "thinking",
				Prefix:      "/thinking",
				Pattern:     `/thinking(?:\s+|$)`,
				Label:       "Thinking",
				Description: "Set reasoning level: /thinking <low|medium|high>",
				Category:    "commands",
				Enabled:     true,
			},
		},
	}
}

// ThinkingLevels are the accepted reasoning-effort values for /thinking.
// "off" clears the level (provider uses its own default).
var ThinkingLevels = []string{"low", "medium", "high", "off"}

func (c CommandsConfig) WithDefaults() CommandsConfig {
	defaults := DefaultCommands()
	if len(c.TriggerChars) == 0 && len(c.Registry) == 0 {
		return defaults
	}
	if len(c.TriggerChars) == 0 {
		c.TriggerChars = []string{"/"}
	}
	seen := map[string]bool{}
	for _, entry := range c.Registry {
		seen[entry.ID] = true
	}
	for _, entry := range defaults.Registry {
		if !seen[entry.ID] {
			c.Registry = append(c.Registry, entry)
		}
	}
	return c
}

func (c CommandsConfig) Validate() error {
	for _, entry := range c.Registry {
		if entry.ID == "" {
			return fmt.Errorf("command id is required")
		}
		if !strings.HasPrefix(entry.Prefix, "/") {
			return fmt.Errorf("command %s prefix must start with /", entry.ID)
		}
		if _, err := regexp.Compile(entry.Pattern); err != nil {
			return fmt.Errorf("command %s pattern: %w", entry.ID, err)
		}
	}
	return nil
}

func (c CommandsConfig) Suggest(query string) []CommandEntry {
	return c.SuggestWithProviders(query, nil)
}

func (c CommandsConfig) SuggestWithProviders(query string, providers []LLMBridge) []CommandEntry {
	c = c.WithDefaults()
	needle := "/" + strings.TrimPrefix(query, "/")
	out := make([]CommandEntry, 0, len(c.Registry)+len(providers))
	// Argument suggestions kick in as soon as the command name is fully typed
	// (e.g. "/model"), not only after a trailing space ("/model "). The needle
	// after the command name - empty when there is no space yet - filters the
	// candidates by prefix.
	if needle == "/model" || strings.HasPrefix(needle, "/model ") {
		providerNeedle := strings.TrimSpace(strings.TrimPrefix(needle, "/model"))
		for _, provider := range providers {
			if provider.Key == "" || !strings.HasPrefix(provider.Key, providerNeedle) {
				continue
			}
			out = append(out, CommandEntry{
				ID:          "model",
				Prefix:      "/model " + provider.Key,
				Pattern:     `/model(?:\s+|$)`,
				Label:       "Model: " + provider.Key,
				Description: provider.Driver + " · " + provider.Model,
				Category:    "models",
				Enabled:     true,
			})
		}
		return out
	}
	if needle == "/thinking" || strings.HasPrefix(needle, "/thinking ") {
		levelNeedle := strings.TrimSpace(strings.TrimPrefix(needle, "/thinking"))
		for _, level := range ThinkingLevels {
			if levelNeedle != "" && !strings.HasPrefix(level, levelNeedle) {
				continue
			}
			out = append(out, CommandEntry{
				ID:          "thinking",
				Prefix:      "/thinking " + level,
				Pattern:     `/thinking(?:\s+|$)`,
				Label:       "Thinking: " + level,
				Description: "Set reasoning level to " + level,
				Category:    "thinking",
				Enabled:     true,
			})
		}
		return out
	}
	for _, entry := range c.Registry {
		if !entry.Enabled {
			continue
		}
		if strings.HasPrefix(entry.Prefix, needle) {
			out = append(out, entry)
		}
	}
	return out
}
