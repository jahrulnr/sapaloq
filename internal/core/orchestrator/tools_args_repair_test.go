package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseToolArgsRawNewlines is the regression for the orch-task heredoc
// failure: a model emitted exec arguments whose "command" value contained RAW
// (unescaped) newline bytes — invalid JSON. parseToolArgs used to ignore the
// unmarshal error and return an empty command, so exec answered "command is
// required" and the model wrongly concluded its content was stripped. With the
// control-char repair, the full multi-line command must survive.
func TestParseToolArgsRawNewlines(t *testing.T) {
	raw := json.RawMessage("{\"command\":\"cat > f <<X\n<!DOCTYPE html>\n<html></html>\nX\"}")
	args := parseToolArgs(raw)
	if args.Command == "" {
		t.Fatal("command was lost (empty) for raw-newline JSON")
	}
	for _, want := range []string{"<<X", "<!DOCTYPE html>", "<html></html>"} {
		if !strings.Contains(args.Command, want) {
			t.Fatalf("command missing %q; got %q", want, args.Command)
		}
	}
}

// TestToolExecWritesMultilineFileViaHeredoc is an end-to-end check that a
// heredoc with real line breaks (the exact pattern the sub-agent tried) now
// actually writes the multi-line file.
func TestToolExecWritesMultilineFileViaHeredoc(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.html")
	cmd := "cat > " + target + " <<'HTMLEOF'\n<!DOCTYPE html>\n<html>\n<body>hi</body>\n</html>\nHTMLEOF\necho wrote"

	raw := json.RawMessage(jsonObjectWithCommand(cmd))
	args := parseToolArgs(raw)
	if args.Command == "" {
		t.Fatal("command empty after parse")
	}
	out := toolExec(context.Background(), args)
	if !strings.Contains(out, "wrote") {
		t.Fatalf("exec did not run as expected: %q", out)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	got := string(body)
	for _, want := range []string{"<!DOCTYPE html>", "<html>", "<body>hi</body>", "</html>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("written file missing %q; got:\n%s", want, got)
		}
	}
}

// jsonObjectWithCommand builds a JSON object that embeds a command value with
// RAW newline bytes (mimicking what a model emits inline), exercising the
// repair path in parseToolArgs.
func jsonObjectWithCommand(cmd string) string {
	// Intentionally NOT json.Marshal — we want the raw, technically-invalid
	// control bytes inside the string value, exactly like the upstream bug.
	return "{\"command\":\"" + strings.ReplaceAll(cmd, "\"", "\\\"") + "\"}"
}
