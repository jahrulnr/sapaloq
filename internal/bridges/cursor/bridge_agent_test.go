package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func TestEmitMCPToolUpdateCompleted(t *testing.T) {
	out := make(chan bridge.StreamEvent, 1)
	emitMCPToolUpdate(
		context.Background(),
		out,
		"sess-1",
		Schema{},
		"glob",
		"tc-1",
		json.RawMessage(`{"pattern":"*.css"}`),
		"found.css\n",
		nil,
	)
	ev := <-out
	if ev.Kind != bridge.EventToolUpdate {
		t.Fatalf("kind = %s", ev.Kind)
	}
	if ev.Status != "completed" {
		t.Fatalf("status = %q", ev.Status)
	}
	if ev.ToolCall == nil || ev.ToolCall.ID != "tc-1" || ev.ToolCall.Name != "glob" {
		t.Fatalf("tool call = %+v", ev.ToolCall)
	}
	if ev.ToolResult != "found.css\n" {
		t.Fatalf("result = %q", ev.ToolResult)
	}
}

func TestEmitMCPToolUpdateFailed(t *testing.T) {
	out := make(chan bridge.StreamEvent, 1)
	emitMCPToolUpdate(
		context.Background(),
		out,
		"sess-1",
		Schema{},
		"glob",
		"tc-2",
		json.RawMessage(`{}`),
		"",
		errors.New("pattern is required"),
	)
	ev := <-out
	if ev.Status != "failed" {
		t.Fatalf("status = %q", ev.Status)
	}
	if ev.ToolResult != "pattern is required" {
		t.Fatalf("result = %q", ev.ToolResult)
	}
}
