package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SetSessionWorkspace persists the Ask actor cwd for a chat session. Relative
// paths and ~ prefixes are expanded; the target must exist and be a directory.
// Returns the normalized absolute directory path on success.
func (o *Orchestrator) SetSessionWorkspace(ctx context.Context, sessionID, path string) (string, error) {
	if o == nil {
		return "", fmt.Errorf("orchestrator unavailable")
	}
	if strings.TrimSpace(sessionID) == "" {
		var err error
		sessionID, err = o.ActiveSession(ctx)
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("session_id is required")
	}
	cleaned := configExpandHome(strings.TrimSpace(path))
	if cleaned == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("path must be absolute")
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return "", fmt.Errorf("path not accessible: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory")
	}
	o.persistActorCWD(sessionID, cleaned)
	return cleaned, nil
}
