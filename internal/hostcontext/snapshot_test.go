package hostcontext

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/privacyfilter"
)

func TestParseRawDegraded(t *testing.T) {
	if ParseRaw(nil) != nil {
		t.Fatal("nil raw should be nil")
	}
	if ParseRaw(json.RawMessage(`{not json`)) != nil {
		t.Fatal("invalid json should be nil")
	}
	if ParseRaw(json.RawMessage(`{"version":99}`)) != nil {
		t.Fatal("unsupported version should be nil")
	}
}

func TestNormalizeAndRender(t *testing.T) {
	raw := json.RawMessage(`{
		"version": 1,
		"workspace": {"session_workspace": "/home/me/proj"},
		"attachments": [{"path": "/home/me/proj/a.go", "kind": "file", "name": "a.go"}],
		"ui": {"mode": "orchestrator", "compose_attachment_count": 1},
		"host": {"os": "linux"}
	}`)
	snap := ParseRaw(raw)
	if snap == nil {
		t.Fatal("parse failed")
	}
	norm := Normalize(snap, DefaultLimits(), privacyfilter.New())
	block := Render(norm)
	for _, want := range []string{
		"# Host context (ephemeral)",
		"session_workspace=/home/me/proj",
		"active_file=none",
		"attachment_paths=/home/me/proj/a.go",
		"ui_mode=orchestrator",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("missing %q in:\n%s", want, block)
		}
	}
}

func TestIsEmpty(t *testing.T) {
	if !(&Snapshot{}).IsEmpty() {
		t.Fatal("empty snapshot should be empty")
	}
	s := &Snapshot{Workspace: Workspace{SessionWorkspace: "/tmp"}}
	if s.IsEmpty() {
		t.Fatal("workspace should not be empty")
	}
}

func TestAttachmentPathsDedup(t *testing.T) {
	s := &Snapshot{Attachments: []Attachment{
		{Path: "/a"},
		{Path: "/a"},
		{Path: "/b"},
	}}
	got := s.AttachmentPaths()
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Fatalf("got %v", got)
	}
}
