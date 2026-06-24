package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// wantDefaultSkillCount is the number of skill folders shipped under
// defaults/. Bump it whenever a default skill is added or removed.
const wantDefaultSkillCount = 3

// TestSeedMaterializesDefaults proves the embedded defaults are written to disk
// with their folder structure preserved, and that a freshly seeded dir loads as
// the two default skills.
func TestSeedMaterializesDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := Seed(dir); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// SKILL.md for every default must exist.
	for _, rel := range []string{
		"frontend-design/SKILL.md",
		"skill-creator/SKILL.md",
		"code-styleguides/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected seeded file %s: %v", rel, err)
		}
	}

	// At least one bundled resource (nested folder) must be materialized too,
	// proving the full tree - not just SKILL.md - is seeded.
	if _, err := os.Stat(filepath.Join(dir, "skill-creator", "scripts", "init_skill.py")); err != nil {
		t.Fatalf("bundled script not seeded: %v", err)
	}

	// Manifest must be created.
	if _, err := os.Stat(filepath.Join(dir, seedManifestName)); err != nil {
		t.Fatalf("manifest not created: %v", err)
	}

	// And the seeded dir loads with all default skills present.
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after seed: %v", err)
	}
	if len(got) != wantDefaultSkillCount {
		t.Fatalf("want %d seeded skills, got %d (%+v)", wantDefaultSkillCount, len(got), ids(got))
	}
	byID := map[string]bool{}
	for _, s := range got {
		byID[s.ID] = true
	}
	for _, want := range []string{"frontend-design", "skill-creator", "code-styleguides"} {
		if !byID[want] {
			t.Fatalf("missing expected default id %q: %v", want, ids(got))
		}
	}
}

// TestSeedIsIdempotent proves repeated seeding is safe: no error and the skill
// count stays stable.
func TestSeedIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := Seed(dir); err != nil {
		t.Fatalf("Seed #1: %v", err)
	}
	if err := Seed(dir); err != nil {
		t.Fatalf("Seed #2: %v", err)
	}
	got, _ := Load(dir)
	if len(got) != wantDefaultSkillCount {
		t.Fatalf("idempotent seed should keep %d skills, got %d", wantDefaultSkillCount, len(got))
	}
}

// TestSeedNeverClobbersUserEdits proves a user-modified seeded file is left
// untouched on the next Seed.
func TestSeedNeverClobbersUserEdits(t *testing.T) {
	dir := t.TempDir()
	if err := Seed(dir); err != nil {
		t.Fatalf("Seed #1: %v", err)
	}
	target := filepath.Join(dir, "frontend-design", "SKILL.md")
	const edited = "---\nname: frontend-design\ndescription: MY OWN EDITED VERSION\n---\n# edited body\n"
	if err := os.WriteFile(target, []byte(edited), 0o644); err != nil {
		t.Fatalf("user edit: %v", err)
	}

	if err := Seed(dir); err != nil {
		t.Fatalf("Seed #2: %v", err)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after re-seed: %v", err)
	}
	if string(after) != edited {
		t.Fatalf("Seed clobbered a user edit; got:\n%s", string(after))
	}
}

func ids(s []Skill) []string {
	out := make([]string, 0, len(s))
	for _, sk := range s {
		out = append(out, sk.ID)
	}
	return out
}
