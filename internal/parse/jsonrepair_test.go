package parse

import (
	"encoding/json"
	"testing"
)

func TestRepairControlCharsInJSON(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantCmd string // expected "command" value after unmarshal (empty = skip)
	}{
		{
			name:    "raw newline inside string value",
			in:      "{\"command\":\"line1\nline2\"}",
			wantCmd: "line1\nline2",
		},
		{
			name:    "heredoc body with multiple raw newlines",
			in:      "{\"command\":\"cat > f <<X\n<!DOCTYPE html>\n<html>\nX\"}",
			wantCmd: "cat > f <<X\n<!DOCTYPE html>\n<html>\nX",
		},
		{
			name:    "raw tab and carriage return",
			in:      "{\"command\":\"a\tb\r\nc\"}",
			wantCmd: "a\tb\r\nc",
		},
		{
			name:    "already-valid escaped JSON is unchanged",
			in:      `{"command":"line1\nline2"}`,
			wantCmd: "line1\nline2",
		},
		{
			name:    "escaped quote inside string keeps boundaries",
			in:      "{\"command\":\"echo \\\"hi\nthere\\\"\"}",
			wantCmd: "echo \"hi\nthere\"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := RepairControlCharsInJSON([]byte(tc.in))
			if !json.Valid(out) {
				t.Fatalf("repaired output is not valid JSON: %q", string(out))
			}
			var got struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("unmarshal repaired JSON: %v", err)
			}
			if got.Command != tc.wantCmd {
				t.Errorf("command=%q want %q", got.Command, tc.wantCmd)
			}
		})
	}
}

// TestRepairControlCharsLeavesStructureOutsideStrings ensures control bytes
// BETWEEN tokens (where JSON allows them as whitespace) and overall structure
// are not corrupted, and that a control byte outside a string isn't escaped
// into a string.
func TestRepairControlCharsLeavesStructureOutsideStrings(t *testing.T) {
	// Pretty-printed JSON (newlines between tokens) must round-trip unchanged
	// in meaning.
	in := "{\n  \"a\": 1,\n  \"b\": \"x\ny\"\n}"
	out := RepairControlCharsInJSON([]byte(in))
	if !json.Valid(out) {
		t.Fatalf("not valid: %q", string(out))
	}
	var got struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.A != 1 || got.B != "x\ny" {
		t.Errorf("got a=%d b=%q", got.A, got.B)
	}
}

// TestRepairControlCharsNoOpOnValid verifies the fast path returns the input
// untouched for already-valid JSON.
func TestRepairControlCharsNoOpOnValid(t *testing.T) {
	in := []byte(`{"x":"y","n":2}`)
	out := RepairControlCharsInJSON(in)
	if string(out) != string(in) {
		t.Fatalf("valid JSON must be unchanged: got %q", string(out))
	}
}
