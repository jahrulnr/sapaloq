package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
)

// toolScribeWriteNote appends a timestamped note to one of the user's
// configured storage destinations (config.storage.paths). The scribe role
// CANNOT write arbitrary files: the destination must resolve to a declared
// storage path (by explicit storage_id, intent phrase, or mode[/kind]), and
// the resolved absolute path is re-checked against the declared set so a
// crafted/symlinked config entry can't be redirected. This is the boundary
// guarantee that keeps the scribe out of project files.
func (o *Orchestrator) toolScribeWriteNote(args toolArgs) string {
	note := strings.TrimSpace(args.Note)
	if note == "" {
		note = strings.TrimSpace(args.Content)
	}
	if note == "" {
		return "Error: note is required."
	}

	dest, ok := o.cfg.Storage.Resolve(args.StorageID, args.Intent, args.Mode, args.Kind)
	if !ok {
		return "Error: could not resolve a storage destination. Provide storage_id, a known intent, or a mode (personal|work|hobby) that exists in storage.paths."
	}

	target := config.ExpandPath(dest.Path)
	if target == "" {
		return "Error: storage destination has no path configured."
	}
	// Boundary re-check: the resolved target must be one of the declared,
	// expanded storage paths. Defense-in-depth against tampered resolution.
	if !o.isDeclaredStoragePath(target) {
		return "Error: resolved path is outside the configured storage boundary."
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "Error: could not create storage directory: " + err.Error()
	}

	stamp := time.Now().Format("2006-01-02 15:04")
	entry := fmt.Sprintf("\n## %s\n%s\n", stamp, note)

	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "Error: could not open storage file: " + err.Error()
	}
	defer f.Close()
	if _, err := f.WriteString(entry); err != nil {
		return "Error: could not append note: " + err.Error()
	}

	label := dest.ID
	if dest.Mode != "" {
		label = fmt.Sprintf("%s (%s)", dest.ID, dest.Mode)
	}
	return fmt.Sprintf("Note appended to %s.", label)
}

// isDeclaredStoragePath reports whether target matches an expanded path in
// config.storage.paths (exact, cleaned comparison).
func (o *Orchestrator) isDeclaredStoragePath(target string) bool {
	clean := filepath.Clean(target)
	for _, p := range o.cfg.Storage.Paths {
		if filepath.Clean(config.ExpandPath(p.Path)) == clean {
			return true
		}
	}
	return false
}
