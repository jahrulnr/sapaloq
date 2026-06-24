package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// TestLeakScannerReassemblesFragmentedInlineCall is the bridge-level regression
// for the "big tool call lost" bug: a model emits a tool call inline in its
// content channel, streamed across many small deltas, with a large argument
// (a whole file body containing unbalanced `{`/`}` inside the string). The
// scanner must accumulate across deltas and emit exactly one reassembled call.
func TestLeakScannerReassemblesFragmentedInlineCall(t *testing.T) {
	body := strings.Repeat("body { color: red } .a{ if(x){y} /* } */\n", 150)
	argBody, _ := json.Marshal(body)
	full := `Sure, writing it now: {"name":"create_file","arguments":{"path":"/tmp/p/index.html","content":` +
		string(argBody) + `}} done.`

	s := newLeakScanner([]string{"create_file", "exec"})
	var got []string
	for i := 0; i < len(full); i += 5 { // tiny fragments, like SSE
		end := i + 5
		if end > len(full) {
			end = len(full)
		}
		for _, tc := range s.feed(full[i:end]) {
			got = append(got, tc.Name)
			if !strings.Contains(string(tc.Arguments), "/tmp/p/index.html") {
				t.Fatalf("reassembled args lost the path: %.60q", string(tc.Arguments))
			}
		}
	}
	got = appendNames(got, s.flush())

	if len(got) != 1 {
		t.Fatalf("want exactly 1 reassembled tool call, got %d (%v)", len(got), got)
	}
	if got[0] != "create_file" {
		t.Fatalf("name=%q want create_file", got[0])
	}
}

// TestLeakScannerReassemblesLabeledInlineCall is the bridge-level regression for
// the orch-chat leak where the model emitted `[Tool: exec]\n{"command":...}` in
// its content channel and it surfaced as a response_delta instead of a tool
// call. The scanner must recover the bracketed-label form, streamed across many
// tiny deltas (the way the real SSE stream split it).
func TestLeakScannerReassemblesLabeledInlineCall(t *testing.T) {
	full := "Sip, lagi kukerjain di background.\n[Tool: exec]\n{\"command\":\"ls -lah /tmp/profile/\"}"

	s := newLeakScanner([]string{"exec", "create_file"})
	var got []parse.ToolCall
	for i := 0; i < len(full); i += 4 { // tiny fragments, like SSE
		end := i + 4
		if end > len(full) {
			end = len(full)
		}
		got = append(got, s.feed(full[i:end])...)
	}
	got = append(got, s.flush()...)

	if len(got) != 1 {
		t.Fatalf("want exactly 1 reassembled labeled tool call, got %d (%v)", len(got), got)
	}
	if got[0].Name != "exec" {
		t.Fatalf("name=%q want exec", got[0].Name)
	}
	if !strings.Contains(string(got[0].Arguments), "ls -lah /tmp/profile/") {
		t.Fatalf("reassembled args lost the command: %q", string(got[0].Arguments))
	}
}

// TestLeakScannerIgnoresUndeclaredAndDisabled verifies the false-positive guard
// (only declared tool names match) and that an empty declared list disables the
// scanner entirely (so it never invents calls from arbitrary JSON).
func TestLeakScannerIgnoresUndeclaredAndDisabled(t *testing.T) {
	text := `here is config {"name":"my-service","arguments":{"port":8080}} ok`

	s := newLeakScanner([]string{"create_file"})
	if calls := s.feed(text); len(calls) != 0 {
		t.Fatalf("undeclared name must not match, got %d", len(calls))
	}

	disabled := newLeakScanner(nil)
	if calls := disabled.feed(`{"name":"create_file","arguments":{}}`); len(calls) != 0 {
		t.Fatalf("scanner with no declared tools must be disabled, got %d", len(calls))
	}
}

func appendNames(dst []string, calls []parse.ToolCall) []string {
	for _, c := range calls {
		dst = append(dst, c.Name)
	}
	return dst
}

// TestLeakScannerReassemblesXMLInvokeCall is the bridge-level regression for
// the "Opus stuck mid-task" bug: an OpenAI-compatible proxy fronting Claude
// leaked the model's native tool_use as Anthropic-style <invoke> XML into the
// content channel (see orch-task progress logs). The JSON scanner did not
// recognise it, so the call was dropped, no tool result came back, and the
// model concluded its tools were dead. The scanner must now reassemble the XML
// call streamed across many tiny SSE deltas.
func TestLeakScannerReassemblesXMLInvokeCall(t *testing.T) {
	full := "The calls are not returning output. Let me retry.\n" +
		"<invoke name=\"exec\">\n<parameter name=\"command\">cat /tmp/profileTool/js/scene.js</parameter>\n</invoke>\n"

	s := newLeakScanner([]string{"exec", "create_file", "write_file"})
	var got []parse.ToolCall
	for i := 0; i < len(full); i += 4 { // tiny fragments, like SSE
		end := i + 4
		if end > len(full) {
			end = len(full)
		}
		got = append(got, s.feed(full[i:end])...)
	}
	got = append(got, s.flush()...)

	if len(got) != 1 {
		t.Fatalf("want exactly 1 reassembled XML tool call, got %d (%v)", len(got), got)
	}
	if got[0].Name != "exec" {
		t.Fatalf("name=%q want exec", got[0].Name)
	}
	if !strings.Contains(string(got[0].Arguments), "/tmp/profileTool/js/scene.js") {
		t.Fatalf("reassembled args lost the command: %q", string(got[0].Arguments))
	}
}

// TestLeakScannerXMLAndJSONInterleaved proves both leak shapes can be recovered
// from the same stream without losing or duplicating either.
func TestLeakScannerXMLAndJSONInterleaved(t *testing.T) {
	full := `<invoke name="exec"><parameter name="command">ls</parameter></invoke> and ` +
		`{"name":"read_file","arguments":{"path":"/tmp/a.txt"}} done`

	s := newLeakScanner([]string{"exec", "read_file"})
	var got []string
	for i := 0; i < len(full); i += 3 {
		end := i + 3
		if end > len(full) {
			end = len(full)
		}
		got = appendNames(got, s.feed(full[i:end]))
	}
	got = appendNames(got, s.flush())

	if len(got) != 2 {
		t.Fatalf("want 2 calls (xml exec + json read_file), got %d (%v)", len(got), got)
	}
	if got[0] != "exec" || got[1] != "read_file" {
		t.Fatalf("order/names wrong: %v", got)
	}
}
