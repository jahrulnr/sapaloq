package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write skill %s: %v", name, err)
	}
}

// writeFolderSkill writes a folder-style skill: <dir>/<folder>/SKILL.md, the
// Anthropic/OpenAI layout.
func writeFolderSkill(t *testing.T, dir, folder, content string) {
	t.Helper()
	sub := filepath.Join(dir, folder)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir skill folder %s: %v", folder, err)
	}
	if err := os.WriteFile(filepath.Join(sub, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write SKILL.md in %s: %v", folder, err)
	}
}

func hasTrigger(triggers []string, want string) bool {
	for _, t := range triggers {
		if t == want {
			return true
		}
	}
	return false
}

// TestLoadFolderSkillNameDescription proves the Anthropic/OpenAI layout works:
// a folder with a SKILL.md using name/description frontmatter loads, ID falls
// back to name, the description is captured, and triggers are mined from the
// name + description so the skill can actually fire.
func TestLoadFolderSkillNameDescription(t *testing.T) {
	dir := t.TempDir()
	writeFolderSkill(t, dir, "frontend-design", `---
name: frontend-design
description: Create distinctive production-grade frontend interfaces. Use when building web components, pages, dashboards, themes, palettes or OKLCH color tokens.
---
# Frontend Design
- Build with OKLCH and semantic tokens.`)

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 skill, got %d (%+v)", len(got), got)
	}
	sk := got[0]
	if sk.ID != "frontend-design" {
		t.Fatalf("ID should fall back to name; got %q", sk.ID)
	}
	if !strings.Contains(sk.Description, "production-grade frontend") {
		t.Fatalf("description not captured: %q", sk.Description)
	}
	if !strings.Contains(sk.Body, "OKLCH and semantic tokens") {
		t.Fatalf("body not captured: %q", sk.Body)
	}
	// Triggers mined from name + description.
	if !hasTrigger(sk.Triggers, "frontend") || !hasTrigger(sk.Triggers, "design") {
		t.Fatalf("name-derived triggers missing: %v", sk.Triggers)
	}
	if !hasTrigger(sk.Triggers, "palettes") && !hasTrigger(sk.Triggers, "dashboards") {
		t.Fatalf("description-derived triggers missing: %v", sk.Triggers)
	}
	// And it actually matches a relevant message (mined name/description words).
	if m := Match(got, "help me with the frontend design of this page"); len(m) != 1 {
		t.Fatalf("folder skill should match a relevant message; got %+v", m)
	}
}

// TestLoadFolderSkillRecoversBrokenFrontmatter proves a SKILL.md whose
// frontmatter was never closed with a "---" still loads: the body begins at the
// first real Markdown heading instead of swallowing the whole file.
func TestLoadFolderSkillRecoversBrokenFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeFolderSkill(t, dir, "skill-creator", `---

## name: skill-creator

description: Guide for creating effective skills. Used when users want to create or update a skill.
metadata:
  short-description: Create or update a skill

# Skill Creator

This skill provides guidance for creating effective skills.`)

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 skill, got %d (%+v)", len(got), got)
	}
	sk := got[0]
	if sk.ID != "skill-creator" {
		t.Fatalf("ID should be derived from name; got %q", sk.ID)
	}
	if !strings.Contains(sk.Body, "guidance for creating effective skills") {
		t.Fatalf("body not recovered after broken frontmatter: %q", sk.Body)
	}
	if strings.Contains(sk.Body, "short-description") {
		t.Fatalf("frontmatter leaked into body: %q", sk.Body)
	}
}

// TestLoadFolderSkillExplicitIDAndTriggersWin confirms an explicit id/triggers
// in a folder skill override the name fallback and the mined triggers.
func TestLoadFolderSkillExplicitIDAndTriggersWin(t *testing.T) {
	dir := t.TempDir()
	writeFolderSkill(t, dir, "anything", `---
id: my-id
name: ignored-name
triggers: [onlythis]
description: some description with manywords here
---
body`)

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].ID != "my-id" {
		t.Fatalf("explicit id should win: %+v", got)
	}
	if len(got[0].Triggers) != 1 || got[0].Triggers[0] != "onlythis" {
		t.Fatalf("explicit triggers should win (no mining): %v", got[0].Triggers)
	}
}

// TestLoadFolderSkillFolderNameFallback confirms ID falls back to the folder
// name when neither id nor name is present.
func TestLoadFolderSkillFolderNameFallback(t *testing.T) {
	dir := t.TempDir()
	writeFolderSkill(t, dir, "from-folder", `---
description: no id and no name here
---
body`)

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].ID != "from-folder" {
		t.Fatalf("ID should fall back to folder name; got %+v", got)
	}
}

// TestRenderIncludesDescription confirms a skill's description is rendered above
// its body so the model sees the "when to use" hint.
func TestRenderIncludesDescription(t *testing.T) {
	sk := Skill{ID: "x", Description: "Use for foo and bar.", Body: "line1\nline2"}
	out := sk.Render(40)
	if !strings.Contains(out, "Use for foo and bar.") {
		t.Fatalf("render should include description: %q", out)
	}
	if !strings.Contains(out, "line1") {
		t.Fatalf("render should include body: %q", out)
	}
}

func TestLoadParsesFrontmatterAndBody(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "scribe.md", `---
id: sapaloq-scribe
triggers: [catat, note this]
priority: 10
maxBodyLines: 40
---
# When capturing notes
- Resolve destination via storage.intents before writing.`)
	writeSkill(t, dir, "dashed.md", `---
id: dashed-skill
triggers:
  - alpha
  - "beta gamma"
---
body text here`)

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 skills, got %d", len(got))
	}
	byID := map[string]Skill{}
	for _, s := range got {
		byID[s.ID] = s
	}
	scribe, ok := byID["sapaloq-scribe"]
	if !ok {
		t.Fatalf("missing sapaloq-scribe")
	}
	if scribe.Priority != 10 || scribe.MaxBodyLines != 40 {
		t.Fatalf("scribe meta wrong: %+v", scribe)
	}
	if len(scribe.Triggers) != 2 || scribe.Triggers[0] != "catat" {
		t.Fatalf("scribe triggers wrong: %v", scribe.Triggers)
	}
	if !strings.Contains(scribe.Body, "storage.intents") {
		t.Fatalf("scribe body missing: %q", scribe.Body)
	}
	dashed := byID["dashed-skill"]
	if len(dashed.Triggers) != 2 || dashed.Triggers[1] != "beta gamma" {
		t.Fatalf("dashed triggers wrong: %v", dashed.Triggers)
	}
}

func TestLoadMissingDirIsInert(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should be inert, got err: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil skills, got %v", got)
	}
	if g, _ := Load(""); g != nil {
		t.Fatalf("empty dir should be inert")
	}
}

func TestLoadSkipsFilesWithoutID(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "noid.md", `---
triggers: [x]
---
body`)
	writeSkill(t, dir, "ok.md", `---
id: ok
---
body`)
	writeSkill(t, dir, "ignore.txt", `id: nope`)

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ok" {
		t.Fatalf("want only [ok], got %+v", got)
	}
}

func TestMatchTriggerSubstringCaseInsensitive(t *testing.T) {
	skills := []Skill{
		{ID: "a", Triggers: []string{"Catat"}},
		{ID: "b", Triggers: []string{"deploy"}},
	}
	got := Match(skills, "tolong CATAT ini ya")
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("want [a], got %+v", got)
	}
	if g := Match(skills, "no trigger here"); g != nil {
		t.Fatalf("want no match, got %+v", g)
	}
	if g := Match(skills, ""); g != nil {
		t.Fatalf("empty message should not match")
	}
}

func TestSortByRelevanceAndCap(t *testing.T) {
	skills := []Skill{
		{ID: "low", Priority: 1},
		{ID: "high", Priority: 9},
		{ID: "mid", Priority: 5},
	}
	got := SortByRelevance(skills, 2)
	if len(got) != 2 || got[0].ID != "high" || got[1].ID != "mid" {
		t.Fatalf("sort/cap wrong: %+v", got)
	}
}

func TestRenderRespectsMaxBodyLines(t *testing.T) {
	sk := Skill{
		ID:           "x",
		MaxBodyLines: 2,
		Body:         "line1\nline2\nline3\nline4",
	}
	out := sk.Render(40)
	if !strings.HasPrefix(out, "### x") {
		t.Fatalf("missing heading: %q", out)
	}
	if strings.Contains(out, "line3") {
		t.Fatalf("should cap at 2 body lines: %q", out)
	}
	// global cap tighter than per-skill cap wins
	out2 := sk.Render(1)
	if strings.Contains(out2, "line2") {
		t.Fatalf("global cap should win: %q", out2)
	}
}
