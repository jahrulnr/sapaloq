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
		Registry: []CommandEntry{{
			ID:          "settings",
			Prefix:      "/settings",
			Pattern:     `/settings(?:\s+|$)`,
			Label:       "Settings",
			Description: "Patch config.json",
			Category:    "commands",
			Enabled:     true,
		}},
	}
}

func (c CommandsConfig) WithDefaults() CommandsConfig {
	if len(c.TriggerChars) == 0 && len(c.Registry) == 0 {
		return DefaultCommands()
	}
	if len(c.TriggerChars) == 0 {
		c.TriggerChars = []string{"/"}
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
	c = c.WithDefaults()
	needle := "/" + strings.TrimPrefix(query, "/")
	out := make([]CommandEntry, 0, len(c.Registry))
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
