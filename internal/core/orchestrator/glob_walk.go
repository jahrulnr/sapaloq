package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gobwas/glob"
	ignore "github.com/sabhiram/go-gitignore"
)

// globSkipDirNames are directories pruned during walk (ripgrep-style: never
// descend into heavy VCS/deps trees even when not listed in .gitignore).
var globSkipDirNames = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
}

// globWalk lists files under root matching pattern. Design follows ripgrep file
// listing principles (not a port of rg):
//   - compiled glob matchers (gobwas/glob), not per-file regex — rg uses globset
//   - prune ignored paths via .gitignore at root + hardcoded dep dirs
//   - forward-slash paths (gitignore / rg convention)
//   - early stop at limit (cap) — avoid scanning entire monorepos for 40 hits
//
// rg is faster mainly because it parallelizes traversal, mmap + SIMD for
// content search, and applies nested gitignore per directory. We adopt the
// pruning + compiled-glob parts that matter most for path listing.
func globWalk(root, pattern string, limit int) ([]string, error) {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	root = filepath.Clean(root)

	matchers, err := compileGlobMatchers(pattern)
	if err != nil {
		return nil, err
	}

	var ig *ignore.GitIgnore
	if gi, err := ignore.CompileIgnoreFile(filepath.Join(root, ".gitignore")); err == nil {
		ig = gi
	}

	w := globWalker{
		root:     root,
		matchers: matchers,
		limit:    limit,
		ignore:   ig,
	}
	_ = filepath.WalkDir(root, w.visit)
	return w.matches, nil
}

type globWalker struct {
	root     string
	matchers []glob.Glob
	limit    int
	matches  []string
	ignore   *ignore.GitIgnore
}

func compileGlobMatchers(pattern string) ([]glob.Glob, error) {
	var matchers []glob.Glob
	seen := make(map[string]bool)
	add := func(p string) error {
		if p == "" || seen[p] {
			return nil
		}
		g, err := glob.Compile(p, '/')
		if err != nil {
			return err
		}
		seen[p] = true
		matchers = append(matchers, g)
		return nil
	}
	if err := add(pattern); err != nil {
		return nil, err
	}
	// gobwas treats ** as "one or more path segments"; rg-style ** at the
	// start also matches files at the search root (e.g. **/*.go → a.go).
	if strings.HasPrefix(pattern, "**/") {
		if err := add(strings.TrimPrefix(pattern, "**/")); err != nil {
			return nil, err
		}
	}
	return matchers, nil
}

func (w *globWalker) visit(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return nil
	}
	if len(w.matches) >= w.limit {
		return filepath.SkipAll
	}

	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return nil
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return nil
	}

	if d.IsDir() {
		name := d.Name()
		if globSkipDirNames[name] {
			return filepath.SkipDir
		}
		if w.ignored(rel+"/", true) {
			return filepath.SkipDir
		}
		return nil
	}

	if w.ignored(rel, false) {
		return nil
	}
	if w.matchesPattern(rel) {
		w.matches = append(w.matches, rel)
		if len(w.matches) >= w.limit {
			return filepath.SkipAll
		}
	}
	return nil
}

func (w *globWalker) ignored(rel string, isDir bool) bool {
	if w.ignore == nil {
		return false
	}
	check := rel
	if isDir && !strings.HasSuffix(check, "/") {
		check += "/"
	}
	return w.ignore.MatchesPath(check)
}

func (w *globWalker) matchesPattern(rel string) bool {
	for _, m := range w.matchers {
		if m.Match(rel) {
			return true
		}
	}
	return false
}

func formatGlobMatches(matches []string, limit int) string {
	if len(matches) == 0 {
		return "No files match."
	}
	sort.Strings(matches)
	out := strings.Join(matches, "\n")
	if len(matches) >= limit {
		out += "\n[capped at " + strconv.Itoa(limit) + " results]"
	}
	return out
}
