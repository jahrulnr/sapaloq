package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MigrateDefaultDataRoot moves non-config runtime artifacts from the old
// ~/.config/sapaloq layout into ~/SapaLOQ. config.json and .env intentionally
// remain under ~/.config/sapaloq. The migration is idempotent and never
// overwrites a destination that already exists.
func MigrateDefaultDataRoot() error {
	oldRoot := filepath.Join(DefaultConfigDir())
	newRoot := DefaultDataDir()
	if oldRoot == "" || newRoot == "" || oldRoot == newRoot {
		return nil
	}
	names := []string{
		"memory", "state", "run", "vault", "workspace", "prompts", "skills",
		"nodes", "bridge", "cache", "prompt", "widget", "os.json",
	}
	var errs []error
	for _, name := range names {
		from := filepath.Join(oldRoot, name)
		to := filepath.Join(newRoot, name)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		if _, err := os.Stat(to); err == nil {
			continue
		}
		if err := movePath(from, to); err != nil {
			errs = append(errs, fmt.Errorf("migrate data root %s -> %s: %w", from, to, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	msg := "data-root migration had issues:"
	for _, err := range errs {
		msg += "\n  - " + err.Error()
	}
	return fmt.Errorf("%s", msg)
}

// MigrateLegacyLayout moves runtime/orchestration artifacts that older versions
// stored under <dataDir>/memory into the dedicated <dataDir>/state directory.
//
// Historically the "memory" dir was a catch-all: it held companion.db (true
// durable memory) alongside transient orchestration state (tasks/, workers/,
// progress/, events.jsonl). The layout was split so "memory" means only the
// companion DB and "state" holds everything transient.
//
// This migration is best-effort and idempotent: each entry is only moved when
// the legacy path exists and the new path does not. companion.db and its WAL/SHM
// sidecars are intentionally left in place. Errors are returned for the caller
// to log; callers should not treat a migration failure as fatal.
func MigrateLegacyLayout(dirs RuntimeDirsInfo) error {
	if dirs.MemoryDir == "" || dirs.StateDir == "" {
		return nil
	}
	moves := []struct{ from, to string }{
		{filepath.Join(dirs.MemoryDir, "tasks"), dirs.TasksDir},
		{filepath.Join(dirs.MemoryDir, "workers"), dirs.WorkersDir},
		{filepath.Join(dirs.MemoryDir, "progress"), dirs.RolloutDir},
		{filepath.Join(dirs.StateDir, "progress"), dirs.RolloutDir},
		{filepath.Join(dirs.MemoryDir, "events.jsonl"), filepath.Join(dirs.StateDir, "events.jsonl")},
	}
	var errs []error
	for _, m := range moves {
		if m.from == "" || m.to == "" || m.from == m.to {
			continue
		}
		if _, err := os.Stat(m.from); err != nil {
			continue // nothing to migrate
		}
		if _, err := os.Stat(m.to); err == nil {
			continue // already migrated; never clobber
		}
		if err := movePath(m.from, m.to); err != nil {
			errs = append(errs, fmt.Errorf("migrate %s -> %s: %w", m.from, m.to, err))
		}
	}
	if len(errs) > 0 {
		// Combine into a single error; callers log it but keep running.
		msg := "legacy layout migration had issues:"
		for _, e := range errs {
			msg += "\n  - " + e.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// movePath renames src to dst, falling back to a recursive copy+remove when the
// two live on different filesystems (os.Rename returns EXDEV).
func movePath(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Cross-device or other rename failure: deep copy then remove the original.
	if err := copyPath(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	return copyFile(src, dst, info.Mode().Perm())
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
