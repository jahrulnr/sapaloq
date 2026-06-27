package config

import (
	"bytes"
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
			// Merge non-conflicting children into the live destination. Any
			// divergent leftovers are archived under the new root instead of being
			// silently stranded in ~/.config/sapaloq forever.
			_ = mergeDirContents(from, to)
			if _, remainErr := os.Stat(from); remainErr == nil {
				archive := filepath.Join(newRoot, "migration-archive", name)
				if _, archiveErr := os.Stat(archive); archiveErr == nil {
					errs = append(errs, fmt.Errorf("migration archive already exists for %s; preserved source at %s", name, from))
					continue
				}
				if moveErr := movePath(from, archive); moveErr != nil {
					errs = append(errs, fmt.Errorf("archive conflicting %s: %w", name, moveErr))
				}
			}
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
	// A short-lived JSON-store regression wrote durable facts/cache beneath
	// state/memory instead of the documented durable memory root. Merge those
	// files before handling the older state split. Existing destination files are
	// never overwritten: identical duplicates are removed, divergent conflicts
	// remain at the source and are reported to the operator.
	if err := mergeDirContents(filepath.Join(dirs.StateDir, "memory"), dirs.MemoryDir); err != nil {
		errs = append(errs, fmt.Errorf("migrate misplaced durable memory: %w", err))
	}
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

func mergeDirContents(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return err
	}
	var conflicts []string
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name())
		if _, statErr := os.Stat(dst); os.IsNotExist(statErr) {
			if err := movePath(src, dst); err != nil {
				return err
			}
			continue
		} else if statErr != nil {
			return statErr
		}
		equal, cmpErr := pathsEqual(src, dst)
		if cmpErr != nil {
			return cmpErr
		}
		if !equal {
			if err := resolveFileConflict(src, dst); err != nil {
				conflicts = append(conflicts, entry.Name())
				continue
			}
			continue
		}
		if err := os.RemoveAll(src); err != nil {
			return err
		}
	}
	remaining, _ := os.ReadDir(srcDir)
	if len(remaining) == 0 {
		_ = os.Remove(srcDir)
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("destination already contains different files; preserved source conflicts: %v", conflicts)
	}
	return nil
}

// resolveFileConflict picks the newer of two differing files at src and dst.
// The winner is kept at dst; the loser is removed. Equal modtimes are left
// unresolved so the operator can inspect both copies.
func resolveFileConflict(src, dst string) error {
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	di, err := os.Stat(dst)
	if err != nil {
		return err
	}
	if si.IsDir() || di.IsDir() {
		return fmt.Errorf("directory conflict")
	}
	switch {
	case si.ModTime().After(di.ModTime()):
		if err := copyPath(src, dst); err != nil {
			return err
		}
	case di.ModTime().After(si.ModTime()):
		// Destination is authoritative; drop the stale misplaced copy.
	default:
		return fmt.Errorf("same modtime")
	}
	return os.Remove(src)
}

func pathsEqual(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if ai.IsDir() || bi.IsDir() {
		return false, nil
	}
	ab, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ab, bb), nil
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
