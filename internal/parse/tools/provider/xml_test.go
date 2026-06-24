package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func known(names ...string) func(string) bool {
	set := map[string]struct{}{}
	for _, n := range names {
		set[n] = struct{}{}
	}
	return func(n string) bool { _, ok := set[n]; return ok }
}

// argString unmarshals a tool call's arguments and returns the named string
// field, failing the test if it is missing or not a string.
func argString(t *testing.T, args json.RawMessage, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		t.Fatalf("args not valid JSON: %v (%s)", err, string(args))
	}
	raw, ok := m[key]
	if !ok {
		t.Fatalf("args missing key %q: %s", key, string(args))
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("key %q not a string: %s", key, string(raw))
	}
	return s
}

// TestXMLInvokeSingleParam covers the canonical leaked Anthropic tool call:
// <invoke name="exec"><parameter name="command">...</parameter></invoke>.
func TestXMLInvokeSingleParam(t *testing.T) {
	text := `Let me check.
<invoke name="exec">
<parameter name="command">cat /tmp/profileTool/js/scene.js</parameter>
</invoke>`
	tc, _, ok := scanXMLInvokeFrom(text, 0, known("exec"))
	if !ok {
		t.Fatal("expected an XML invoke match")
	}
	if tc.Name != "exec" {
		t.Fatalf("name=%q want exec", tc.Name)
	}
	if got := argString(t, tc.Arguments, "command"); got != "cat /tmp/profileTool/js/scene.js" {
		t.Fatalf("command=%q", got)
	}
	if !json.Valid(tc.Arguments) {
		t.Fatalf("arguments not valid JSON: %s", string(tc.Arguments))
	}
}

// TestXMLInvokeMultiParamMultiline covers write_file with a multi-line body
// (real newlines + braces), the shape that broke the JSON path historically.
func TestXMLInvokeMultiParamMultiline(t *testing.T) {
	body := "function f(){\n  return { a: 1 };\n}\n"
	text := `<invoke name="write_file">
<parameter name="path">/tmp/x/app.js</parameter>
<parameter name="content">` + body + `</parameter>
</invoke>`
	tc, _, ok := scanXMLInvokeFrom(text, 0, known("write_file"))
	if !ok {
		t.Fatal("expected a match")
	}
	if tc.Name != "write_file" {
		t.Fatalf("name=%q", tc.Name)
	}
	if !json.Valid(tc.Arguments) {
		t.Fatalf("args not valid JSON: %s", string(tc.Arguments))
	}
	if p := argString(t, tc.Arguments, "path"); p != "/tmp/x/app.js" {
		t.Fatalf("path=%q", p)
	}
	if c := argString(t, tc.Arguments, "content"); !strings.Contains(c, "return { a: 1 };") {
		t.Fatalf("content lost braces/newlines: %q", c)
	}
}

// TestXMLInvokeUndeclaredIgnored is the false-positive guard: a recognised XML
// shape naming an undeclared tool must NOT be treated as a call.
func TestXMLInvokeUndeclaredIgnored(t *testing.T) {
	text := `<invoke name="definitely_not_a_tool"><parameter name="x">1</parameter></invoke>`
	if _, _, ok := scanXMLInvokeFrom(text, 0, known("exec")); ok {
		t.Fatal("undeclared tool should be ignored")
	}
}

// TestXMLInvokePartialHoldsFrontier proves the streaming contract: an opener
// with no </invoke> yet reports ok=false and a frontier at the opener so the
// caller retains the partial block.
func TestXMLInvokePartialHoldsFrontier(t *testing.T) {
	text := `blah <invoke name="exec"><parameter name="command">slee`
	tc, next, ok := scanXMLInvokeFrom(text, 0, known("exec"))
	if ok {
		t.Fatalf("incomplete block should not match, got %+v", tc)
	}
	openIdx := strings.Index(text, "<invoke")
	if next != openIdx {
		t.Fatalf("frontier=%d want %d (the opener)", next, openIdx)
	}
}

// TestParseToolCallLeakXMLEndToEnd checks integration through the public
// ParseToolCallLeakFrom: XML and JSON shapes coexist and both are recovered.
func TestParseToolCallLeakXMLEndToEnd(t *testing.T) {
	text := `<invoke name="exec"><parameter name="command">echo hi</parameter></invoke> then {"name":"read_file","arguments":{"path":"/a"}}`
	k := known("exec", "read_file")

	tc1, next1, ok1 := ParseToolCallLeakFrom(text, 0, k)
	if !ok1 || tc1.Name != "exec" {
		t.Fatalf("first call: ok=%v name=%q", ok1, tc1.Name)
	}
	tc2, _, ok2 := ParseToolCallLeakFrom(text, next1, k)
	if !ok2 || tc2.Name != "read_file" {
		t.Fatalf("second call: ok=%v name=%q", ok2, tc2.Name)
	}
}

// TestXMLAttrSingleQuote verifies single-quoted attributes parse too.
func TestXMLAttrSingleQuote(t *testing.T) {
	tc, _, ok := scanXMLInvokeFrom(`<invoke name='exec'><parameter name='command'>ls</parameter></invoke>`, 0, known("exec"))
	if !ok || tc.Name != "exec" {
		t.Fatalf("single-quote attr failed: ok=%v name=%q", ok, tc.Name)
	}
	if argString(t, tc.Arguments, "command") != "ls" {
		t.Fatalf("command parse failed: %s", string(tc.Arguments))
	}
}

// TestCoerceParamValueJSONKept ensures a numeric/JSON value is preserved as a
// JSON value, not double-encoded as a string.
func TestCoerceParamValueJSONKept(t *testing.T) {
	text := `<invoke name="set_limit"><parameter name="n">42</parameter></invoke>`
	tc, _, ok := scanXMLInvokeFrom(text, 0, known("set_limit"))
	if !ok {
		t.Fatal("expected match")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(tc.Arguments, &m); err != nil {
		t.Fatalf("invalid args: %v", err)
	}
	if strings.TrimSpace(string(m["n"])) != "42" {
		t.Fatalf("numeric value not kept as JSON number: %s", string(m["n"]))
	}
}

var _ = parse.ToolCall{}
