package skills

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// defaultFS holds the skills that ship embedded in the binary. They are
// materialized to disk on first run (see Seed) so the user can edit them and so
// folder-bundled resources (references/, scripts/, assets/, agents/) are
// available at runtime. The `all:` prefix ensures dotfiles and every nested
// file/dir under defaults/ are embedded.
//
//go:embed all:defaults
var defaultFS embed.FS

// seedManifestName is the sha256 manifest recording the hash SapaLOQ shipped for
// each seeded file, enabling "upgrade-if-unmodified" without clobbering user
// edits — the same contract the prompts package uses.
const seedManifestName = "skills.manifest.json"

// Seed materializes the embedded default skills into dir. For each shipped file
// (relative path preserved):
//   - absent on disk → write it and record its shipped hash;
//   - present and unmodified (disk hash == recorded shipped hash) but the
//     shipped default changed → upgrade in place and update the manifest;
//   - present and modified by the user → keep it untouched (never clobber);
//   - present with no manifest record → adopt the disk content and record its
//     hash so future upgrades only apply while the user hasn't edited it.
//
// The manifest itself lives at <dir>/skills.manifest.json. Seed is idempotent
// (safe to call every boot) and best-effort: a disk error is returned but must
// not be treated as fatal by callers — skill seeding never breaks startup.
func Seed(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	manifestPath := filepath.Join(dir, seedManifestName)
	manifest := loadSeedManifest(manifestPath)

	var firstErr error
	walkErr := fs.WalkDir(defaultFS, "defaults", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, "defaults/")
		if rel == "" || rel == p {
			return nil
		}
		data, readErr := defaultFS.ReadFile(p)
		if readErr != nil {
			if firstErr == nil {
				firstErr = readErr
			}
			return nil
		}
		// Mark scripts executable; everything else is regular content.
		mode := os.FileMode(0o644)
		if strings.Contains(rel, "/scripts/") && (strings.HasSuffix(rel, ".py") || strings.HasSuffix(rel, ".sh")) {
			mode = 0o755
		}
		if err := seedFile(filepath.Join(dir, filepath.FromSlash(rel)), rel, data, mode, manifest); err != nil && firstErr == nil {
			firstErr = err
		}
		return nil
	})
	if walkErr != nil && firstErr == nil {
		firstErr = walkErr
	}

	if err := saveSeedManifest(manifestPath, manifest); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// seedFile applies the upgrade-if-unmodified / never-clobber policy for one
// embedded file and updates the manifest entry (keyed by the slash relative
// path) in place.
func seedFile(absPath, relKey string, def []byte, mode os.FileMode, manifest map[string]string) error {
	defHash := hashBytes(def)
	onDisk, readErr := os.ReadFile(absPath)
	switch {
	case os.IsNotExist(readErr):
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(absPath, def, mode); err != nil {
			return err
		}
		manifest[relKey] = defHash
		return nil
	case readErr != nil:
		// Unreadable on disk this boot: leave it, don't fight the filesystem.
		return readErr
	default:
		diskHash := hashBytes(onDisk)
		shipped := manifest[relKey]
		if shipped != "" && diskHash == shipped && shipped != defHash {
			// Unmodified by user AND shipped default changed → upgrade.
			if err := os.WriteFile(absPath, def, mode); err != nil {
				return err
			}
			manifest[relKey] = defHash
			return nil
		}
		if shipped == "" {
			// Pre-existing file with no record: adopt it so a future upgrade
			// only applies while the user hasn't edited it since.
			manifest[relKey] = diskHash
		}
		return nil
	}
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func loadSeedManifest(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]string{}
	}
	return out
}

func saveSeedManifest(path string, man map[string]string) error {
	b, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
