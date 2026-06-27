package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestExecWriteTargetPath(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{`cat > /tmp/profile/index.html <<'EOF'`, "/tmp/profile/index.html"},
		{`cat >> ~/notes.txt`, mustExpandHome("~/notes.txt")},
		{`tee /var/log/out.log`, "/var/log/out.log"},
		{`uname -a`, ""},
		{`mkdir -p /tmp/profile/css`, ""},
	}
	for _, tc := range tests {
		got := execWriteTargetPath(tc.cmd)
		if got != tc.want {
			t.Fatalf("execWriteTargetPath(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

func mustExpandHome(path string) string {
	return expandHome(path)
}

func TestToolResourceKeyExecWriteUsesPathLane(t *testing.T) {
	call := parseToolCall(t, "exec", `{"command":"cat > /tmp/a.html <<'EOF'"}`)
	key := toolResourceKey("run-1", call)
	if key != "path:/tmp/a.html" {
		t.Fatalf("key = %q, want path:/tmp/a.html", key)
	}
}

func TestToolResourceKeyExecOpaqueUsesRunLane(t *testing.T) {
	call := parseToolCall(t, "exec", `{"command":"uname -a"}`)
	key := toolResourceKey("run-42", call)
	if key != "exec:run-42" {
		t.Fatalf("key = %q, want exec:run-42", key)
	}
}

func parseToolCall(t *testing.T, name, args string) parse.ToolCall {
	t.Helper()
	return parse.ToolCall{Name: name, Arguments: json.RawMessage(args)}
}
