package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetFallsBackToEmbeddedDefaults(t *testing.T) {
	m := New("", false) // disabled, no dir → embedded defaults only
	for _, role := range []string{RoleAsk, RolePlanner, RoleAgent, RoleScribe} {
		if got := m.Get(role); strings.TrimSpace(got) == "" {
			t.Fatalf("Get(%q) returned empty; expected embedded default", role)
		}
	}
	if m.Get("nonexistent") != "" {
		t.Fatalf("unknown role should return empty")
	}
	// task-runner is an alias of agent.
	if m.Get("task-runner") != m.Get(RoleAgent) {
		t.Fatalf("task-runner should alias agent")
	}
}

func TestSyncSeedsDefaultsWithManifest(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, true)
	for _, file := range []string{"ask.md", "planner.md", "agent.md", "scribe.md"} {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Fatalf("expected %s seeded: %v", file, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, manifestName)); err != nil {
		t.Fatalf("expected manifest written: %v", err)
	}
	man := m.loadManifest()
	if man["ask.md"] == "" {
		t.Fatalf("manifest missing ask.md hash")
	}
}

func TestUserModifiedPromptIsKept(t *testing.T) {
	dir := t.TempDir()
	_ = New(dir, true) // seed
	askPath := filepath.Join(dir, "ask.md")
	custom := "MY CUSTOM ASK PROMPT\n"
	if err := os.WriteFile(askPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	// Re-sync: user-modified file must be kept (hash != shipped).
	m2 := New(dir, true)
	if got := m2.Get(RoleAsk); got != custom {
		t.Fatalf("expected user-modified prompt kept, got %q", got)
	}
	onDisk, _ := os.ReadFile(askPath)
	if string(onDisk) != custom {
		t.Fatalf("user prompt overwritten on disk")
	}
}

func TestUnmodifiedPromptUpgradesWhenDefaultChanges(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, true) // seed with current default
	askPath := filepath.Join(dir, "ask.md")

	// Simulate an OLD shipped default: rewrite the file to an old value AND
	// record that old value as the shipped hash in the manifest (i.e. the user
	// never touched it; it just predates the current default).
	old := "OLD DEFAULT ASK PROMPT\n"
	if err := os.WriteFile(askPath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	man := m.loadManifest()
	man["ask.md"] = hash(old)
	if err := m.saveManifest(man); err != nil {
		t.Fatal(err)
	}

	// Re-sync: file is "unmodified" (matches recorded shipped hash) but the
	// embedded default differs → it should upgrade to the embedded default.
	m2 := New(dir, true)
	got := m2.Get(RoleAsk)
	if got == old {
		t.Fatalf("expected unmodified prompt to upgrade to new default, still old")
	}
	if got != Default(RoleAsk) {
		t.Fatalf("upgraded prompt should equal embedded default")
	}
}
