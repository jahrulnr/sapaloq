package wire

import (
	"context"
	"testing"
)

func TestDecodeExecServerEventRequestContext(t *testing.T) {
	payload := buildExecServerVariant(esmRequestContextArgs, nil, 7, "exec-1")
	ev, ok := DecodeExecServerEvent(payload)
	if !ok {
		t.Fatal("expected exec event")
	}
	if ev.Kind != "exec_request_context" || ev.ExecMsgID != 7 || ev.ExecID != "exec-1" {
		t.Fatalf("ev = %+v", ev)
	}
}

func TestDecodeExecServerEventMCP(t *testing.T) {
	valueBytes := encodeField(valString, wireLen, "uptime")
	entry := concat(
		encodeField(mapKey, wireLen, "command"),
		encodeFieldLen(mapValue, valueBytes),
	)
	argsBody := concat(
		encodeField(mcaToolName, wireLen, "exec"),
		encodeFieldLen(mcaArgs, entry),
	)
	payload := buildExecServerVariant(esmMCPArgs, argsBody, 3, "mcp-1")
	ev, ok := DecodeExecServerEvent(payload)
	if !ok {
		t.Fatal("expected mcp exec event")
	}
	if ev.Kind != "exec_mcp" || ev.ExecMsgID != 3 || ev.ExecID != "mcp-1" {
		t.Fatalf("ev = %+v", ev)
	}
	if ev.Args["command"] != "uptime" {
		t.Fatalf("args = %+v, tool=%q", ev.Args, ev.ToolName)
	}
	if ev.ToolName != "exec" {
		t.Fatalf("tool name = %q", ev.ToolName)
	}
}

func TestBuildExecRejectionRead(t *testing.T) {
	frame, ok := BuildExecRejection(ExecServerEvent{
		Kind:      "exec_read",
		ExecMsgID: 1,
		ExecID:    "x",
		Path:      "/etc/passwd",
	})
	if !ok || len(frame) < 6 {
		t.Fatalf("frame = %d bytes, ok=%v", len(frame), ok)
	}
}

func TestHandleExecMCPUsesExecutor(t *testing.T) {
	var written [][]byte
	state := &agentStreamState{
		ctx: context.Background(),
		ackedExec: map[string]struct{}{},
		mcpExecutor: func(_ context.Context, name, id string, args map[string]any) (string, bool, error) {
			if name != "exec" || id != "tc-1" {
				t.Fatalf("unexpected tool %q id %q", name, id)
			}
			if args["command"] != "echo hi" {
				t.Fatalf("args = %+v", args)
			}
			return "hi\n", false, nil
		},
		writeFrame: func(frame []byte) error {
			written = append(written, append([]byte(nil), frame...))
			return nil
		},
	}
	ev := ExecServerEvent{
		Kind:       "exec_mcp",
		ExecMsgID:  9,
		ExecID:     "exec-mcp",
		ToolName:   "exec",
		ToolCallID: "tc-1",
		Args:       map[string]any{"command": "echo hi"},
	}
	if err := state.handleExecEvent(context.Background(), ev); err != nil {
		t.Fatalf("handleExecEvent: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("writes = %d, want 1 MCP result frame", len(written))
	}
}

func TestHandleExecBuiltinRejection(t *testing.T) {
	var written [][]byte
	state := &agentStreamState{
		ctx:       context.Background(),
		ackedExec: map[string]struct{}{},
		writeFrame: func(frame []byte) error {
			written = append(written, frame)
			return nil
		},
	}
	ev := ExecServerEvent{Kind: "exec_shell", ExecMsgID: 2, ExecID: "sh", Command: "rm -rf /"}
	if err := state.handleExecEvent(context.Background(), ev); err != nil {
		t.Fatalf("handleExecEvent: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("writes = %d, want rejection frame", len(written))
	}
}

func buildExecServerVariant(variantField int, variantBody []byte, execMsgID int, execID string) []byte {
	inner := concat(
		encodeField(esmID, wireVarint, uint64(execMsgID)),
		encodeField(esmExecID, wireLen, execID),
	)
	if variantBody != nil {
		inner = concat(inner, encodeFieldLen(variantField, variantBody))
	} else {
		inner = concat(inner, encodeFieldLen(variantField, nil))
	}
	esm := encodeFieldLen(asmExecServerMessage, inner)
	return esm
}
