package orchestrator

import (
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestResolveToolCallByID(t *testing.T) {
	var traces []toolCallTrace
	trackToolCall(&traces, parse.ToolCall{ID: "tc-1", Name: "glob", Arguments: []byte(`{"pattern":"*"}`)})
	resolveToolCall(&traces, &parse.ToolCall{ID: "tc-1", Name: "glob"})
	if unresolved := unresolvedToolCalls(traces); len(unresolved) != 0 {
		t.Fatalf("expected resolved, got %v", unresolved)
	}
}

func TestMalformedToolFailureResultShowsRawArgs(t *testing.T) {
	got := malformedToolFailureResult(parse.ToolCall{
		Name:      "glob_file_search",
		Source:    "openai_inline",
		Arguments: []byte(`{"glob_pattern":"**/*.{js,tsx}"}`),
	})
	for _, want := range []string{"glob_file_search", "openai_inline", "**/*.{js,tsx}"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
