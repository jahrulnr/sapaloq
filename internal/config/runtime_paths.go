package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ProductionSocketRel is the default runtime IPC socket under ~/SapaLOQ.
	ProductionSocketRel = "run/sapaloq.sock"
	// TestSocketFileName is the basename for isolated go test / e2e harness sockets.
	TestSocketFileName = "sapaloq-test.sock"
	// MockSocketRel is the repo-local dev mock socket (make mock / sapaloq-mock).
	MockSocketRel = ".sapaloq/run/sapaloq-mock.sock"
)

// ProductionSocketPath returns the expanded default production unix socket.
func ProductionSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ExpandPath("~/SapaLOQ/" + ProductionSocketRel)
	}
	return filepath.Join(home, "SapaLOQ", ProductionSocketRel)
}

// IsProductionSocketPath reports whether path resolves to the live user socket.
func IsProductionSocketPath(path string) bool {
	path = filepath.Clean(ExpandPath(strings.TrimSpace(path)))
	prod := filepath.Clean(ProductionSocketPath())
	return path != "" && path == prod
}

// RepoMockSocketPath returns <repo>/.sapaloq/run/sapaloq-mock.sock.
func RepoMockSocketPath() (string, error) {
	root, err := FindRepoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, MockSocketRel), nil
}

// FindRepoRoot walks upward from cwd (then the executable dir) until go.mod exists.
func FindRepoRoot() (string, error) {
	candidates := []string{}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, start := range candidates {
		start = filepath.Clean(start)
		if _, ok := seen[start]; ok {
			continue
		}
		seen[start] = struct{}{}
		for dir := start; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	return "", os.ErrNotExist
}

// RunningUnderGoTest is true for binaries started by go test.
func RunningUnderGoTest() bool {
	return flag.Lookup("test.v") != nil
}
