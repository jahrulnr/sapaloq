package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestParseToolCallLeak(t *testing.T) {
	// nil `known` = accept any well-formed tool JSON (legacy behavior).
	cases := []struct {
		name   string
		in     string
		wantOK bool
		want   string
	}{
		{
			name:   "valid name+arguments",
			in:     `noise {"name":"search","arguments":{"q":"x"}} more`,
			wantOK: true,
			want:   "search",
		},
		{
			name:   "valid with parameters alias",
			in:     `noise {"name":"echo","parameters":{}} more`,
			wantOK: true,
			want:   "echo",
		},
		{
			name:   "no tool JSON",
			in:     "just a normal response",
			wantOK: false,
		},
		{
			name:   "empty name rejected",
			in:     `{"name":"","arguments":{}}`,
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc2, ok := ParseToolCallLeak(tc.in, nil)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if ok && tc2.Name != tc.want {
				t.Errorf("name=%q want %q", tc2.Name, tc.want)
			}
		})
	}
}

// TestParseToolCallLeakReassemblesAcrossFragments is the core regression for the
// "big tool call lost" bug: a model emits a tool call whose argument is a large
// file body inline in content, streamed across many fragments. Scanning the
// reassembled buffer must recover it (scanning any single fragment cannot).
func TestParseToolCallLeakReassemblesAcrossFragments(t *testing.T) {
	// Body deliberately contains UNBALANCED braces inside the string (a CSS
	// rule with a `{` but no matching `}` nearby, JS object literals, etc.) —
	// the brace scanner must be string-aware or it closes the object early and
	// drops the call. This mirrors a real HTML/CSS/JS file argument.
	body := strings.Repeat("body { color: red; /* } unbalanced { */ } .a{b:c} if(x){y}\n", 200)
	full := `prose before {"name":"create_file","arguments":{"path":"/tmp/p/index.html","content":` +
		mustJSONString(body) + `}} prose after`

	// Split into many small fragments, like an SSE content stream.
	known := func(n string) bool { return n == "create_file" }
	var buf strings.Builder
	var got parse.ToolCall
	var found bool
	for i := 0; i < len(full); i += 7 {
		end := i + 7
		if end > len(full) {
			end = len(full)
		}
		buf.WriteString(full[i:end])
		if tc, _, ok := ParseToolCallLeakFrom(buf.String(), 0, known); ok {
			got, found = tc, true
			break
		}
	}
	if !found {
		t.Fatal("a tool call split across fragments was never reassembled")
	}
	if got.Name != "create_file" {
		t.Fatalf("name=%q want create_file", got.Name)
	}
	if !strings.Contains(string(got.Arguments), "index.html") {
		t.Fatalf("arguments lost the path: %q", string(got.Arguments)[:60])
	}
}

// TestParseToolCallLeakIgnoresUnknownNames guards against false positives: a
// JSON blob inside file content that merely has name+arguments fields must NOT
// be read as a tool call when its name is not a recognised tool.
func TestParseToolCallLeakIgnoresUnknownNames(t *testing.T) {
	in := `here is a config: {"name":"my-app","arguments":{"port":8080}}`
	known := func(n string) bool { return n == "create_file" || n == "exec" }
	if _, ok := ParseToolCallLeak(in, known); ok {
		t.Fatal("unknown name must not be treated as a tool call")
	}
	// But a real tool name in the same shape is accepted.
	in2 := `sure: {"name":"exec","arguments":{"command":"ls"}}`
	if tc, ok := ParseToolCallLeak(in2, known); !ok || tc.Name != "exec" {
		t.Fatalf("known tool name should match: ok=%v name=%q", ok, tc.Name)
	}
}

func mustJSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestHasReasoningLeak(t *testing.T) {
	if !HasReasoningLeak("hello <|channel|>analysis<|message|>x<|end|>") {
		t.Error("channel tag must be detected")
	}
	if !HasReasoningLeak("hello ¹thinkx⁄think⁄") {
		t.Error("legacy tag must be detected")
	}
	if HasReasoningLeak("plain text") {
		t.Error("plain text must not be flagged")
	}
}

func TestStripReasoningFromText(t *testing.T) {
	thinking, cleaned := StripReasoningFromText("before<|channel|>analysis<|message|>deep<|end|>after")
	if thinking != "deep" {
		t.Errorf("thinking: %q", thinking)
	}
	if cleaned != "beforeafter" {
		t.Errorf("cleaned: %q", cleaned)
	}
	thinking, cleaned = StripReasoningFromText("plain")
	if thinking != "" || cleaned != "plain" {
		t.Errorf("plain text must pass through, got thinking=%q cleaned=%q", thinking, cleaned)
	}
}
