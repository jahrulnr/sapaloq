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
