package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandBrowsePathTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := expandBrowsePath("~/proj"); got != filepath.Join(home, "proj") {
		t.Fatalf("expandBrowsePath(~) = %q", got)
	}
}

func TestNormalizeWorkspaceStartDirRejectsLabelText(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := normalizeWorkspaceStartDir("Pilih workspace"); got != home {
		t.Fatalf("normalize = %q, want home %q", got, home)
	}
}

func TestNormalizeWorkspaceStartDirKeepsValidPath(t *testing.T) {
	root := t.TempDir()
	if got := normalizeWorkspaceStartDir(root); got != root {
		t.Fatalf("normalize = %q, want %q", got, root)
	}
}
