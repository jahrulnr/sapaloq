package main

import (
	"os"
	"path/filepath"
	"strings"
)

func expandBrowsePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Clean(home)
		}
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return filepath.Clean(home)
			}
			return filepath.Clean(filepath.Join(home, path[2:]))
		}
	}
	return filepath.Clean(path)
}

// normalizeWorkspaceStartDir returns an existing absolute directory for dialog defaults.
func normalizeWorkspaceStartDir(startDir string) string {
	cleaned := expandBrowsePath(startDir)
	if cleaned != "" && filepath.IsAbs(cleaned) {
		if info, err := os.Stat(cleaned); err == nil && info.IsDir() {
			return cleaned
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Clean(home)
	}
	return string(filepath.Separator)
}
