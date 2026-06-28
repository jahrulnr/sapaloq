package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

func TestRunTurnFreshDynamicToolAndStreaming(t *testing.T) {
	var sawDynamicTools bool
	endpoint, closeServer := websocketServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var req wireMessage
			if conn.ReadJSON(&req) != nil {
				return
			}
			switch req.Method {
			case "initialize":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
			case "initialized":
			case "thread/start":
				sawDynamicTools = strings.Contains(string(req.Params), `"dynamicTools"`) && strings.Contains(string(req.Params), `"read_file"`)
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"thread-1"}}`)})
			case "turn/start":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"turn-1"}}`)})
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", Method: "turn/started", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-1"}}`)})
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: json.RawMessage(`"tool-request"`), Method: "item/tool/call", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","callId":"call-1","namespace":"sapaloq","tool":"read_file","arguments":{"path":"README.md"}}`)})
			case "":
				if string(req.ID) == `"tool-request"` {
					if !strings.Contains(string(req.Result), `"success":true`) || !strings.Contains(string(req.Result), "tool-result") {
						t.Errorf("dynamic tool result = %s", req.Result)
					}
					_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", Method: "item/agentMessage/delta", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"a1","delta":"answer"}`)})
					_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}`)})
				}
			}
		}
	})
	defer closeServer()

	out := make(chan bridge.StreamEvent, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := RunTurn(ctx, endpoint, TurnRequest{
		SessionID: "session-1", FreshPrompt: "hello", Cwd: t.TempDir(), Sandbox: "workspace-write",
		DynamicTools: []DynamicToolNamespace{{Type: "namespace", Name: "sapaloq", Tools: []DynamicToolFunction{{Type: "function", Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}}}},
		ToolExecutor: func(_ context.Context, call parse.ToolCall) (string, error) {
			if call.Name != "read_file" {
				t.Fatalf("tool name = %q", call.Name)
			}
			return "tool-result", nil
		},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if res.ThreadID != "thread-1" || res.TurnID != "turn-1" || res.Resumed {
		t.Fatalf("result = %+v", res)
	}
	if !sawDynamicTools {
		t.Fatal("thread/start did not advertise dynamic tools")
	}
	close(out)
	var response string
	var sawTool, sawDone bool
	for ev := range out {
		response += ev.Delta
		if ev.Kind == bridge.EventToolCall && ev.ToolCall != nil && ev.ToolCall.Name == "read_file" && ev.ToolCall.Source == "codex" {
			sawTool = true
		}
		sawDone = sawDone || ev.Kind == bridge.EventDone
	}
	if response != "answer" || !sawTool || !sawDone {
		t.Fatalf("events response=%q tool=%v done=%v", response, sawTool, sawDone)
	}
}

func TestRunTurnResumeFailureFallsBackToFreshPrompt(t *testing.T) {
	var turnInput string
	endpoint, closeServer := websocketServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var req wireMessage
			if conn.ReadJSON(&req) != nil {
				return
			}
			switch req.Method {
			case "initialize":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
			case "thread/resume":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: -32602, Message: "thread missing"}})
			case "thread/start":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"fresh-thread"}}`)})
			case "turn/start":
				turnInput = string(req.Params)
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"turn-2"}}`)})
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", Method: "turn/completed", Params: json.RawMessage(`{"threadId":"fresh-thread","turn":{"id":"turn-2","status":"completed"}}`)})
			}
		}
	})
	defer closeServer()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := RunTurn(ctx, endpoint, TurnRequest{SessionID: "s", ResumeThread: "gone", FreshPrompt: "FULL HISTORY", ResumePrompt: "LATEST", Sandbox: "read-only"}, make(chan bridge.StreamEvent, 4))
	if err != nil {
		t.Fatal(err)
	}
	if res.ThreadID != "fresh-thread" || res.Resumed || !strings.Contains(turnInput, "FULL HISTORY") || strings.Contains(turnInput, "LATEST") {
		t.Fatalf("fallback result=%+v input=%s", res, turnInput)
	}
}

func TestRunTurnCancellationSendsInterrupt(t *testing.T) {
	interrupted := make(chan struct{})
	endpoint, closeServer := websocketServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var req wireMessage
			if conn.ReadJSON(&req) != nil {
				return
			}
			switch req.Method {
			case "initialize":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
			case "thread/start":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"t"}}`)})
			case "turn/start":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"u"}}`)})
			case "turn/interrupt":
				close(interrupted)
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
			}
		}
	})
	defer closeServer()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := RunTurn(ctx, endpoint, TurnRequest{SessionID: "s", FreshPrompt: "x", Sandbox: "read-only"}, make(chan bridge.StreamEvent, 4))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	select {
	case <-interrupted:
	case <-time.After(time.Second):
		t.Fatal("turn/interrupt was not sent")
	}
}

func TestDynamicToolFailureReturnsUnsuccessfulToolResponse(t *testing.T) {
	out := make(chan bridge.StreamEvent, 2)
	result, err := handleServerRequest(context.Background(), func(context.Context, parse.ToolCall) (string, error) {
		return "", errors.New("tool failed")
	}, NewMapper("s"), ServerRequest{
		Method: "item/tool/call",
		Params: json.RawMessage(`{"threadId":"t","turnId":"u","callId":"c","namespace":"sapaloq","tool":"read_file","arguments":{}}`),
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := result.(DynamicToolCallResponse)
	if !ok || response.Success || len(response.ContentItems) != 1 || response.ContentItems[0].Text != "tool failed" {
		t.Fatalf("response = %#v", result)
	}
	if ev := <-out; ev.Kind != bridge.EventToolCall || ev.ToolCall == nil || ev.ToolCall.Source != "codex" {
		t.Fatalf("telemetry = %+v", ev)
	}
}

func TestRunTurnFailedTerminalEmitsErrorOnly(t *testing.T) {
	endpoint, closeServer := websocketServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var req wireMessage
			if conn.ReadJSON(&req) != nil {
				return
			}
			switch req.Method {
			case "initialize":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
			case "thread/start":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"t"}}`)})
			case "turn/start":
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"u"}}`)})
				_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", Method: "turn/completed", Params: json.RawMessage(`{"threadId":"t","turn":{"id":"u","status":"failed","error":{"message":"bad turn"}}}`)})
			}
		}
	})
	defer closeServer()
	out := make(chan bridge.StreamEvent, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := RunTurn(ctx, endpoint, TurnRequest{SessionID: "s", FreshPrompt: "x", Sandbox: "read-only"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	var errorsSeen, doneSeen int
	for ev := range out {
		if ev.Kind == bridge.EventError && ev.Error == "bad turn" {
			errorsSeen++
		}
		if ev.Kind == bridge.EventDone {
			doneSeen++
		}
	}
	if errorsSeen != 1 || doneSeen != 0 {
		t.Fatalf("errors=%d done=%d", errorsSeen, doneSeen)
	}
}
