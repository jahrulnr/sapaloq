package kimi

import (
	"encoding/json"
	"testing"
)

func TestParseInlineJSON(t *testing.T) {
	calls := ParseInline(`<|tool_call_begin|>glob_file_search {"pattern":"*.go"}<|tool_call_end|>`)
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].Name != "glob_file_search" {
		t.Fatalf("name = %q", calls[0].Name)
	}
}

func TestParseSectionUnicodeDelimiters(t *testing.T) {
	// cu/default emits fullwidth pipe + block underscore (same as turns.json).
	text := "Exploring.\n\n\u003c｜tool▁calls▁begin｜\u003e\u003c｜tool▁call▁begin｜\u003e\nGlob\n\u003c｜tool▁sep｜\u003etarget_directory\n/home/jahrulnr\n\u003c｜tool▁sep｜\u003eglob_pattern\n**/*\n\u003c｜tool▁call▁end｜\u003e\u003c｜tool▁calls▁end｜\u003e"
	got := ExtractInline(text)
	if len(got.Calls) != 1 {
		t.Fatalf("calls = %d", len(got.Calls))
	}
	if got.Calls[0].Name != "Glob" {
		t.Fatalf("name = %q", got.Calls[0].Name)
	}
	var args map[string]any
	if err := json.Unmarshal(got.Calls[0].Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if args["target_directory"] != "/home/jahrulnr" || args["glob_pattern"] != "**/*" {
		t.Fatalf("args = %#v", args)
	}
	if got.CleanedText != "Exploring." {
		t.Fatalf("cleaned = %q", got.CleanedText)
	}
}

func TestParseSectionShell(t *testing.T) {
	text := `<｜tool▁calls▁begin｜><｜tool▁call▁begin｜>Shell<｜tool▁sep｜>command
echo hi
<｜tool▁sep｜>description
test
<｜tool▁call▁end｜><｜tool▁calls▁end｜>`
	got := ExtractInline(text)
	if len(got.Calls) != 1 || got.Calls[0].Name != "Shell" {
		t.Fatalf("calls = %#v", got.Calls)
	}
	var args map[string]any
	if err := json.Unmarshal(got.Calls[0].Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if args["command"] != "echo hi" {
		t.Fatalf("command = %#v", args["command"])
	}
}
