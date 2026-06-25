package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A dropped folder must come back as a path-only attachment: IsDir set, no
// contents read (neither text nor a data URI), so the prompt is never flooded
// with a whole tree. This is the folder drag-and-drop entry point.
func TestReadDroppedFileDirectoryIsPathOnly(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	// Put a file inside to prove we never read the contents.
	if err := os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := app.ReadDroppedFile(dir)
	if err != nil {
		t.Fatalf("expected directory drop to succeed, got error: %v", err)
	}
	if !got.IsDir {
		t.Fatalf("expected IsDir=true for a folder drop")
	}
	if got.Path != filepath.Clean(dir) {
		t.Fatalf("path = %q, want %q", got.Path, filepath.Clean(dir))
	}
	if got.Name != filepath.Base(dir) {
		t.Fatalf("name = %q, want %q", got.Name, filepath.Base(dir))
	}
	if got.Text != "" || got.DataURI != "" {
		t.Fatalf("folder drop must carry no contents (text=%q dataURI len=%d)", got.Text, len(got.DataURI))
	}
	if got.Size != 0 {
		t.Fatalf("folder drop size = %d, want 0", got.Size)
	}
}

// A dropped text file still returns its contents inline (unchanged behaviour),
// with IsDir explicitly false.
func TestReadDroppedFileTextFileReadsContents(t *testing.T) {
	app := NewApp()
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := app.ReadDroppedFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.IsDir {
		t.Fatalf("file drop must have IsDir=false")
	}
	if got.Text != "hello" {
		t.Fatalf("text = %q, want %q", got.Text, "hello")
	}
}

// A relative path is rejected for both files and folders (defence-in-depth).
func TestReadDroppedFileRejectsRelativePath(t *testing.T) {
	app := NewApp()
	if _, err := app.ReadDroppedFile("relative/dir"); err == nil {
		t.Fatalf("expected error for a relative path")
	}
}
