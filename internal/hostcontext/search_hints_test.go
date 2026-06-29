package hostcontext

import (
	"strings"
	"testing"
)

func TestSearchHintsFromSnapshot(t *testing.T) {
	s := &Snapshot{
		Workspace: Workspace{SessionWorkspace: "/home/me/proj"},
		Attachments: []Attachment{
			{Path: "/home/me/proj/a.go"},
		},
	}
	h := s.SearchHints()
	if h.SessionWorkspace != "/home/me/proj" {
		t.Fatalf("workspace = %q", h.SessionWorkspace)
	}
	if len(h.AttachmentPaths) != 1 || h.AttachmentPaths[0] != "/home/me/proj/a.go" {
		t.Fatalf("paths = %v", h.AttachmentPaths)
	}
}

func TestPrefetchSearchQueryIncludesWorkspaceAndPaths(t *testing.T) {
	q := PrefetchSearchQuery("hello", SearchHints{
		SessionWorkspace: "/projects/foo",
		AttachmentPaths:  []string{"/projects/foo/main.go"},
	})
	for _, want := range []string{"hello", "/projects/foo", "/projects/foo/main.go"} {
		if !strings.Contains(q, want) {
			t.Fatalf("missing %q in %q", want, q)
		}
	}
}

func TestSkillsAugmentQueryDedupesTokens(t *testing.T) {
	got := SkillsAugmentQuery(SearchHints{
		AttachmentPaths: []string{"/projects/foo/main.go"},
	})
	for _, want := range []string{"/projects/foo/main.go", "main.go", ".go", "main"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestSearchHintsCacheDigestDiffersByWorkspace(t *testing.T) {
	paths := []string{"/a/x.go"}
	d1 := (SearchHints{SessionWorkspace: "/proj/a", AttachmentPaths: paths}).CacheDigest()
	d2 := (SearchHints{SessionWorkspace: "/proj/b", AttachmentPaths: paths}).CacheDigest()
	if d1 == "" || d2 == "" || d1 == d2 {
		t.Fatalf("digests should differ: %q vs %q", d1, d2)
	}
}

func TestSearchHintsCacheDigestDiffersByPaths(t *testing.T) {
	ws := "/proj"
	d1 := (SearchHints{SessionWorkspace: ws, AttachmentPaths: []string{"/a/x.go"}}).CacheDigest()
	d2 := (SearchHints{SessionWorkspace: ws, AttachmentPaths: []string{"/b/y.go"}}).CacheDigest()
	if d1 == d2 {
		t.Fatalf("path digests should differ: %q", d1)
	}
}
