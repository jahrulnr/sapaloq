package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestAppendRedactsAndBoundsArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool-calls.jsonl")
	w, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	secret := "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ123456"
	raw, _ := json.Marshal(map[string]string{"command": "curl -H token=" + secret})
	if err := w.Append(Entry{RawName: "exec", Arguments: raw, Reason: "executed"}); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadEntries(path, 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("read: entries=%d err=%v", len(entries), err)
	}
	if string(entries[0].Arguments) == string(raw) || strings.Contains(string(entries[0].Arguments), secret) {
		t.Fatalf("secret argument was not redacted: %s", entries[0].Arguments)
	}

	large := json.RawMessage(`{"content":"` + strings.Repeat("x", maxAuditArgumentsBytes) + `"}`)
	if err := w.Append(Entry{RawName: "create_file", Arguments: large, Reason: "executed"}); err != nil {
		t.Fatal(err)
	}
	entries, _ = ReadEntries(path, 1)
	if len(entries[0].Arguments) >= maxAuditArgumentsBytes {
		t.Fatalf("large arguments were not bounded: %d", len(entries[0].Arguments))
	}
}
