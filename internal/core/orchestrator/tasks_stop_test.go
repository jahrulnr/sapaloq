package orchestrator

import (
	"context"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestSapaloqStopTaskScopeEndsForegroundWhenTaskMissing(t *testing.T) {
	o := &Orchestrator{stateDir: t.TempDir(), sessionTasks: map[string]map[string]struct{}{
		"chat-1": {"task-missing": {}},
	}}
	call := parse.ToolCall{
		Name:      "sapaloq_stop",
		Arguments: []byte(`{"scope":"task","task_id":"task-missing","reason":"acknowledged user"}`),
	}
	out := make(chan bridge.StreamEvent, 1)
	res := o.dispatchAskTool(context.Background(), providerSnapshot{}, out, "chat-1", "", call, parseToolArgs(call.Arguments))
	if !res.handled {
		t.Fatalf("expected handled stop, got %+v", res)
	}
	if !res.stop {
		t.Fatalf("foreground generation must end even when task stop fails, got %+v", res)
	}
}
