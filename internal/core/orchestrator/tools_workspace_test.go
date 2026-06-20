package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdirTemp switches the process CWD (the tool sandbox root) to a fresh temp
// dir for the duration of a test and restores it afterward.
func chdirTemp(t *testing.T) string {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	// Resolve symlinks (macOS /var → /private/var) so comparisons are stable.
	resolved, _ := filepath.EvalSymlinks(dir)
	if resolved != "" {
		return resolved
	}
	return dir
}

func TestReadFileLineRange(t *testing.T) {
	chdirTemp(t)
	if err := os.WriteFile("f.txt", []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := toolReadFile(toolArgs{Path: "f.txt", Offset: 2, Limit: 2})
	if !strings.Contains(out, "2\tb") || !strings.Contains(out, "3\tc") {
		t.Fatalf("expected lines 2-3 with numbers, got:\n%s", out)
	}
	if strings.Contains(out, "1\ta") || strings.Contains(out, "4\td") {
		t.Fatalf("range leaked extra lines:\n%s", out)
	}
}

func TestReadFileBinaryGuard(t *testing.T) {
	chdirTemp(t)
	if err := os.WriteFile("bin", []byte{0x00, 0x01, 0x02, 'a', 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	out := toolReadFile(toolArgs{Path: "bin"})
	if !strings.Contains(out, "binary") {
		t.Fatalf("expected binary refusal, got: %s", out)
	}
}

func TestEditFileUniqueAndAmbiguous(t *testing.T) {
	chdirTemp(t)
	if err := os.WriteFile("e.txt", []byte("hello world\nhello there\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ambiguous: "hello" appears twice → error without replace_all.
	if out := toolEditFile(toolArgs{Path: "e.txt", OldString: "hello", NewString: "hi"}); !strings.Contains(out, "occurs 2 times") {
		t.Fatalf("expected ambiguous error, got: %s", out)
	}

	// Unique replace.
	if out := toolEditFile(toolArgs{Path: "e.txt", OldString: "hello world", NewString: "hi world"}); !strings.Contains(out, "Edited") {
		t.Fatalf("unique edit failed: %s", out)
	}
	got, _ := os.ReadFile("e.txt")
	if string(got) != "hi world\nhello there\n" {
		t.Fatalf("unexpected content: %q", got)
	}

	// replace_all.
	if out := toolEditFile(toolArgs{Path: "e.txt", OldString: "h", NewString: "H", ReplaceAll: true}); !strings.Contains(out, "Edited") {
		t.Fatalf("replace_all failed: %s", out)
	}

	// Not found.
	if out := toolEditFile(toolArgs{Path: "e.txt", OldString: "zzz", NewString: "y"}); !strings.Contains(out, "not found") {
		t.Fatalf("expected not-found, got: %s", out)
	}
}

func TestDeleteFile(t *testing.T) {
	chdirTemp(t)
	if err := os.WriteFile("d.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := toolDeleteFile(toolArgs{Path: "d.txt"}); !strings.Contains(out, "Deleted") {
		t.Fatalf("delete failed: %s", out)
	}
	if _, err := os.Stat("d.txt"); !os.IsNotExist(err) {
		t.Fatalf("file should be gone")
	}
	// Directory is refused.
	if err := os.Mkdir("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if out := toolDeleteFile(toolArgs{Path: "sub"}); !strings.Contains(out, "directory") {
		t.Fatalf("expected directory refusal, got: %s", out)
	}
	// Traversal is rejected.
	if out := toolDeleteFile(toolArgs{Path: "../escape.txt"}); !strings.Contains(out, "outside the workspace") {
		t.Fatalf("expected traversal rejection, got: %s", out)
	}
}

func TestGlob(t *testing.T) {
	chdirTemp(t)
	_ = os.MkdirAll("pkg/sub", 0o755)
	_ = os.WriteFile("a.go", []byte("package a"), 0o644)
	_ = os.WriteFile("pkg/b.go", []byte("package b"), 0o644)
	_ = os.WriteFile("pkg/sub/c.go", []byte("package c"), 0o644)
	_ = os.WriteFile("readme.md", []byte("# r"), 0o644)

	// Recursive **/*.go should find all three .go files.
	out := toolGlob(toolArgs{Pattern: "**/*.go"})
	for _, want := range []string{"a.go", filepath.Join("pkg", "b.go"), filepath.Join("pkg", "sub", "c.go")} {
		if !strings.Contains(out, want) {
			t.Fatalf("glob missing %s in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "readme.md") {
		t.Fatalf("glob should not match readme.md:\n%s", out)
	}
}
