package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool-calls.jsonl")
	w, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Entry{
		Provider:     "cursor-bridge",
		RawName:      "glob",
		ResolvedName: "glob_file_search",
		Reason:       "undeclared",
	}); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entry Entry
	if err := json.Unmarshal(blob[:len(blob)-1], &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Reason != "undeclared" || entry.ResolvedName != "glob_file_search" {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestStatsForGroupsEntries(t *testing.T) {
	entries := []Entry{
		{Provider: "cursor-bridge", RawName: "glob", ResolvedName: "glob_file_search", Reason: "undeclared"},
		{Provider: "cursor-bridge", RawName: "glob", ResolvedName: "glob_file_search", Reason: "undeclared"},
		{Provider: "cursor-bridge", RawName: "x", ResolvedName: "x", Reason: "unknown_upstream"},
	}
	stats := StatsFor(entries)
	if stats.Total != 3 || stats.ByReason["undeclared"] != 2 {
		t.Fatalf("stats = %#v", stats)
	}
	if len(stats.TopTools) == 0 || stats.TopTools[0].Name != "glob_file_search" {
		t.Fatalf("top = %#v", stats.TopTools)
	}
}

func TestReadEntriesLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool-calls.jsonl")
	w, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := w.Append(Entry{RawName: fmt.Sprintf("t%d", i), Reason: "undeclared"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := ReadEntries(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d", len(entries))
	}
}
