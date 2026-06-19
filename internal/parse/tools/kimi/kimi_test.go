package kimi

import "testing"

func TestParseInline(t *testing.T) {
	calls := ParseInline(`<|tool_call_begin|>glob_file_search {"pattern":"*.go"}<|tool_call_end|>`)
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].Name != "glob_file_search" {
		t.Fatalf("name = %q", calls[0].Name)
	}
}
