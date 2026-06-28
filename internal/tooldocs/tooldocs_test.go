package tooldocs

import (
	"strings"
	"testing"
)

func TestParseDescriptionFrontmatter(t *testing.T) {
	const doc = `---
description: |
  Line one of contract.
  Line two of contract.
---
# tool
Body ignored for wire.`
	got := parseDescription(doc)
	want := "Line one of contract. Line two of contract."
	if got != want {
		t.Fatalf("parseDescription() = %q, want %q", got, want)
	}
}

func TestParseDescriptionInline(t *testing.T) {
	const doc = `---
description: "Short one-liner."
---
`
	if got := parseDescription(doc); got != "Short one-liner." {
		t.Fatalf("got %q", got)
	}
}

func TestEveryEmbeddedDocHasDescription(t *testing.T) {
	for _, name := range Names() {
		desc := Description(name)
		if strings.TrimSpace(desc) == "" {
			t.Errorf("tool %q has empty description", name)
		}
		if len(desc) < 40 {
			t.Errorf("tool %q description too short (%d chars): %q", name, len(desc), desc)
		}
	}
}

func TestKnownToolsHaveDocs(t *testing.T) {
	want := []string{
		"read_file", "glob", "edit_file", "delete_file", "search", "list_dir",
		"web_search", "web_fetch", "write_file", "create_file", "exec", "read_image",
		"sapaloq_spawn_plan", "sapaloq_spawn_agent", "write_plan", "read_plan",
		"sapaloq_update_task_progress", "sapaloq_complete_task", "sapaloq_fail_task",
		"request_clarification", "sapaloq_answer_clarification", "sapaloq_spawn_scribe",
		"scribe_write_note", "desktop_notify", "desktop_dnd_status", "wait",
		"sapaloq_cancel_job", "sapaloq_stop", "sapaloq_get_task_status",
		"sapaloq_send_steering", "sapaloq_request_decision",
	}
	have := make(map[string]struct{}, len(Names()))
	for _, n := range Names() {
		have[n] = struct{}{}
	}
	for _, name := range want {
		if _, ok := have[name]; !ok {
			t.Errorf("missing tooldocs file for %q", name)
		}
	}
}
