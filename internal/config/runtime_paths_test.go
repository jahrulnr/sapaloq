package config

import (
	"path/filepath"
	"testing"
)

func TestIsProductionSocketPath(t *testing.T) {
	prod := ProductionSocketPath()
	if !IsProductionSocketPath(prod) {
		t.Fatalf("expected production path %q", prod)
	}
	if IsProductionSocketPath(filepath.Join(t.TempDir(), "run", TestSocketFileName)) {
		t.Fatal("test socket should not match production")
	}
}

func TestRepoMockSocketPathInsideRepo(t *testing.T) {
	root, err := FindRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	mock, err := RepoMockSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(mock) {
		t.Fatalf("mock socket must be absolute: %q", mock)
	}
	rel, err := filepath.Rel(root, mock)
	if err != nil || rel != MockSocketRel {
		t.Fatalf("mock socket = %q rel %q want %q", mock, rel, MockSocketRel)
	}
}

func TestWriteTestConfigIsolatesSocket(t *testing.T) {
	_, _, socket := WriteTestConfig(t, "test")
	if IsProductionSocketPath(socket) {
		t.Fatalf("test harness socket must not be production: %q", socket)
	}
	if filepath.Base(socket) != TestSocketFileName {
		t.Fatalf("basename = %q want %q", filepath.Base(socket), TestSocketFileName)
	}
}
