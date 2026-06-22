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

// TestParseToolCallLabeledForms covers the inline labeled tool-call forms the
// model is actually instructed to emit (see the role prompts), which the older
// scanner ignored — letting them leak into the chat as a response_delta. This
// is the regression for the orch-chat "[Tool: exec]\n{...}" leak.
func TestParseToolCallLabeledForms(t *testing.T) {
	known := func(n string) bool { return n == "exec" || n == "read_file" || n == "create_file" }
	cases := []struct {
		name     string
		in       string
		known    func(string) bool
		wantOK   bool
		wantName string
		wantArg  string // substring expected in arguments
	}{
		{
			name:     "bracketed label with newline (the leaked form)",
			in:       "Sip, dikerjakan.\n[Tool: exec]\n{\"command\":\"ls -lah /tmp/profile/\"}",
			known:    known,
			wantOK:   true,
			wantName: "exec",
			wantArg:  "ls -lah /tmp/profile/",
		},
		{
			name:     "bracketed label is accepted without known set",
			in:       "[Tool: anything]\n{\"x\":1}",
			known:    nil,
			wantOK:   true,
			wantName: "anything",
			wantArg:  "\"x\":1",
		},
		{
			name:     "bracketed label rejected when not a known tool",
			in:       "[Tool: notatool]\n{\"x\":1}",
			known:    known,
			wantOK:   false,
		},
		{
			name:     "bare known-tool name before args",
			in:       "sure: read_file {\"path\":\"/etc/hosts\"}",
			known:    known,
			wantOK:   true,
			wantName: "read_file",
			wantArg:  "/etc/hosts",
		},
		{
			name:   "bare unknown name is not a tool call",
			in:     "the config object {\"port\":8080}",
			known:  known,
			wantOK: false,
		},
		{
			name:   "bare form requires known set (nil rejects)",
			in:     "exec {\"command\":\"ls\"}",
			known:  nil,
			wantOK: false,
		},
		{
			name:   "name as suffix of a longer word is not matched",
			in:     "prefixexec {\"command\":\"ls\"}",
			known:  known,
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseToolCallLeak(tc.in, tc.known)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (got name=%q args=%q)", ok, tc.wantOK, got.Name, string(got.Arguments))
			}
			if !ok {
				return
			}
			if got.Name != tc.wantName {
				t.Errorf("name=%q want %q", got.Name, tc.wantName)
			}
			if tc.wantArg != "" && !strings.Contains(string(got.Arguments), tc.wantArg) {
				t.Errorf("arguments=%q missing %q", string(got.Arguments), tc.wantArg)
			}
		})
	}
}

// TestParseToolCallLabeledReassembledAcrossFragments ensures a bracketed-label
// call whose (large) args object streams across many content deltas is still
// recovered, including when the label itself is split mid-stream.
func TestParseToolCallLabeledReassembledAcrossFragments(t *testing.T) {
	body := strings.Repeat("body { color: red; } .a{b:c} if(x){y}\n", 200)
	full := "intro text\n[Tool: create_file]\n{\"path\":\"/tmp/p/index.html\",\"content\":" +
		mustJSONString(body) + "}"
	known := func(n string) bool { return n == "create_file" }

	var buf strings.Builder
	var got parse.ToolCall
	var found bool
	scanned := 0
	for i := 0; i < len(full); i += 5 {
		end := i + 5
		if end > len(full) {
			end = len(full)
		}
		buf.WriteString(full[i:end])
		tc, next, ok := ParseToolCallLeakFrom(buf.String(), scanned, known)
		if ok {
			got, found = tc, true
			break
		}
		scanned = next
	}
	if !found {
		t.Fatal("labeled tool call split across fragments was never reassembled")
	}
	if got.Name != "create_file" {
		t.Fatalf("name=%q want create_file", got.Name)
	}
	if !strings.Contains(string(got.Arguments), "index.html") {
		t.Fatalf("arguments lost the path: %q", string(got.Arguments))
	}
}

// TestParseToolCallLeakMultilineRawNewlines is the regression for the
// orch-task heredoc failure: a model wrote an inline tool call whose argument
// body had REAL (unescaped) line breaks (a heredoc HTML file). The JSON is
// technically invalid, so the call used to be rejected/lost and the model
// concluded its content was "stripped". The scanner must repair the raw control
// bytes and recover the call with the full multi-line command intact.
func TestParseToolCallLeakMultilineRawNewlines(t *testing.T) {
	known := func(n string) bool { return n == "exec" }

	// Envelope form with a raw newline inside the command value.
	envelope := "ok: {\"name\":\"exec\",\"arguments\":{\"command\":\"cat > f <<X\n<!DOCTYPE html>\n<html></html>\nX\"}}"
	tc, ok := ParseToolCallLeak(envelope, known)
	if !ok {
		t.Fatal("multi-line envelope tool call was not recovered")
	}
	if tc.Name != "exec" {
		t.Fatalf("name=%q want exec", tc.Name)
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		t.Fatalf("recovered arguments are not valid JSON: %v (%q)", err, string(tc.Arguments))
	}
	if !strings.Contains(args.Command, "<!DOCTYPE html>") || !strings.Contains(args.Command, "<<X") {
		t.Fatalf("command lost its multi-line body: %q", args.Command)
	}

	// Bracketed labeled form with raw newlines in the bare args object.
	labeled := "[Tool: exec]\n{\"command\":\"echo a\necho b\"}"
	tc2, ok2 := ParseToolCallLeak(labeled, known)
	if !ok2 {
		t.Fatal("multi-line labeled tool call was not recovered")
	}
	if err := json.Unmarshal(tc2.Arguments, &args); err != nil {
		t.Fatalf("labeled args not valid JSON: %v (%q)", err, string(tc2.Arguments))
	}
	if args.Command != "echo a\necho b" {
		t.Fatalf("labeled command=%q", args.Command)
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
