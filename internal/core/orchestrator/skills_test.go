package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/skills"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

func newSkillsOrch(t *testing.T, enabled bool, maxLoad int, sk []skills.Skill) *Orchestrator {
	t.Helper()
	store, err := chatstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open chat store: %v", err)
	}
	return &Orchestrator{
		cfg: config.Config{
			Skills: config.SkillsConfig{
				Enabled:        enabled,
				Dir:            "/nonexistent",
				MaxLoadPerTurn: maxLoad,
				MaxBodyLines:   40,
			},
		},
		chat:   store,
		skills: sk,
	}
}

func TestSkillsBlockDisabledIsEmpty(t *testing.T) {
	o := newSkillsOrch(t, false, 2, []skills.Skill{
		{ID: "a", Triggers: []string{"catat"}, Body: "do the thing"},
	})
	if got := o.skillsBlock(context.Background(), "tolong catat ini"); got != "" {
		t.Fatalf("disabled skills should yield empty block, got %q", got)
	}
}

func TestSkillsBlockTriggerMatchInjects(t *testing.T) {
	o := newSkillsOrch(t, true, 2, []skills.Skill{
		{ID: "scribe-skill", Triggers: []string{"catat"}, Body: "Resolve destination first."},
		{ID: "other", Triggers: []string{"deploy"}, Body: "ship it"},
	})
	got := o.skillsBlock(context.Background(), "tolong catat ini ya")
	if !strings.Contains(got, "Relevant skills") {
		t.Fatalf("missing header: %q", got)
	}
	if !strings.Contains(got, "scribe-skill") || !strings.Contains(got, "Resolve destination first.") {
		t.Fatalf("matched skill not injected: %q", got)
	}
	if strings.Contains(got, "other") {
		t.Fatalf("non-matching skill should not appear: %q", got)
	}
}

func TestSkillsBlockRespectsMaxLoadPerTurn(t *testing.T) {
	o := newSkillsOrch(t, true, 2, []skills.Skill{
		{ID: "s1", Triggers: []string{"go"}, Priority: 1, Body: "one"},
		{ID: "s2", Triggers: []string{"go"}, Priority: 9, Body: "two"},
		{ID: "s3", Triggers: []string{"go"}, Priority: 5, Body: "three"},
	})
	got := o.skillsBlock(context.Background(), "go now")
	// cap=2, sorted by priority desc => s2, s3
	if !strings.Contains(got, "s2") || !strings.Contains(got, "s3") {
		t.Fatalf("expected top-2 by priority (s2,s3): %q", got)
	}
	if strings.Contains(got, "s1") {
		t.Fatalf("lowest-priority skill should be dropped at cap=2: %q", got)
	}
}

func TestSkillsBlockNoMatchIsEmpty(t *testing.T) {
	o := newSkillsOrch(t, true, 2, []skills.Skill{
		{ID: "a", Triggers: []string{"catat"}, Body: "x"},
	})
	if got := o.skillsBlock(context.Background(), "nothing relevant here"); got != "" {
		t.Fatalf("no trigger + no FTS hit should be empty, got %q", got)
	}
}
