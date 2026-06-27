package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// wantDefaultSkillCount is the number of skill folders shipped under
// defaults/. Bump it whenever a default skill is added or removed.
const wantDefaultSkillCount = 4

// TestSeedMaterializesDefaults proves the embedded defaults are written to disk
// with their folder structure preserved, and that a freshly seeded dir loads as
// the default skills.
func TestSeedMaterializesDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := Seed(dir); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// SKILL.md (and key bundled files) for every default must exist.
	for _, rel := range []string{
		"frontend-design/SKILL.md",
		"skill-creator/SKILL.md",
		"code-styleguides/SKILL.md",
		"peek-agents/SKILL.md",
		"peek-agents/scripts/peek.sh",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected seeded file %s: %v", rel, err)
		}
	}

	// Bundled shell scripts must be seeded executable (the seeder marks
	// scripts/*.sh|*.py with mode 0755 so exec can run them directly).
	if info, err := os.Stat(filepath.Join(dir, "peek-agents", "scripts", "peek.sh")); err != nil {
		t.Fatalf("peek.sh not seeded: %v", err)
	} else if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("peek.sh should be executable, got mode %v", info.Mode().Perm())
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
	for _, want := range []string{"frontend-design", "skill-creator", "code-styleguides", "peek-agents"} {
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

// TestPeekAgentsSkillTriggers proves the shipped peek-agents skill fires on
// representative ID + EN messages a user would use to inspect sub-agents. The
// skill declares no explicit triggers, so this also exercises trigger-mining
// from its name/description.
func TestPeekAgentsSkillTriggers(t *testing.T) {
	dir := t.TempDir()
	if err := Seed(dir); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, msg := range []string{
		"coba intip agent yang lagi jalan",
		"kenapa task itu gagal?",
		"monitor planner dong",
		"check the worker health",
		"which task is awaiting clarification",
	} {
		matched := Match(loaded, msg)
		if !containsID(matched, "peek-agents") {
			t.Fatalf("peek-agents should match %q, got %v", msg, ids(matched))
		}
	}

	// A clearly unrelated message must NOT select it (guards over-broad
	// trigger mining).
	if containsID(Match(loaded, "what's the weather today"), "peek-agents") {
		t.Fatalf("peek-agents should not match an unrelated message")
	}
}

func containsID(s []Skill, id string) bool {
	for _, sk := range s {
		if sk.ID == id {
			return true
		}
	}
	return false
}

func ids(s []Skill) []string {
	out := make([]string, 0, len(s))
	for _, sk := range s {
		out = append(out, sk.ID)
	}
	return out
}
