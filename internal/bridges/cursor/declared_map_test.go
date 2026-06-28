package cursor

import (
	"encoding/json"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestResolveToolCallGlobProductName(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	declared := []string{"glob"}
	call := ResolveToolCall(schema, parse.ToolCall{
		Name:      "Glob",
		Arguments: json.RawMessage(`{"glob_pattern":"**/*.go","target_directory":"/tmp"}`),
	})
	if call.Name != "glob" {
		t.Fatalf("name = %q, want glob", call.Name)
	}
	var args map[string]any
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if args["pattern"] != "**/*.go" || args["path"] != "/tmp" {
		t.Fatalf("args = %#v", args)
	}
	if got := VaultReason(schema, declared, "Glob", call); got != "" {
		t.Fatalf("vault reason = %q, want pass", got)
	}
}

func TestResolveToolCallShellToExec(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	call := ResolveToolCall(schema, parse.ToolCall{
		Name:      "Shell",
		Arguments: json.RawMessage(`{"command":"uptime"}`),
	})
	if call.Name != "exec" {
		t.Fatalf("name = %q", call.Name)
	}
}

func TestResolveToolCallGrepToSearch(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	call := ResolveToolCall(schema, parse.ToolCall{
		Name:      "Grep",
		Arguments: json.RawMessage(`{"pattern":"TODO","path":"/src"}`),
	})
	if call.Name != "search" {
		t.Fatalf("name = %q", call.Name)
	}
}

func TestResolveToolCallWebFetch(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	declared := []string{"web_fetch"}
	call := ResolveToolCall(schema, parse.ToolCall{Name: "WebFetch", Arguments: json.RawMessage(`{"url":"https://example.com"}`)})
	if call.Name != "web_fetch" {
		t.Fatalf("name = %q", call.Name)
	}
	if got := VaultReason(schema, declared, "WebFetch", call); got != "" {
		t.Fatalf("vault = %q", got)
	}
}

func TestResolveToolCallPreservesDeclaredNames(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatal(err)
	}
	call := ResolveToolCall(schema, parse.ToolCall{Name: "sapaloq_stop", Arguments: json.RawMessage(`{"reason":"done"}`)})
	if call.Name != "sapaloq_stop" {
		t.Fatalf("name = %q", call.Name)
	}
}
