package appserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestProbeUnixWebSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: rpcHandler(func(conn *websocket.Conn) {
		defer conn.Close()
		var req wireMessage
		if conn.ReadJSON(&req) != nil || req.Method != "initialize" {
			return
		}
		_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"userAgent":"test","codexHome":"/tmp","platformFamily":"unix","platformOs":"linux"}`)})
		_ = conn.ReadJSON(&req) // initialized notification
	})}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Probe(ctx, "unix://"+path); err != nil {
		t.Fatalf("Probe: %v", err)
	}
}

func TestClientRoutesServerRequestAndResponse(t *testing.T) {
	endpoint, closeServer := websocketServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		var req wireMessage
		_ = conn.ReadJSON(&req)
		_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)})
		_ = conn.ReadJSON(&req) // initialized
		_ = conn.WriteJSON(wireMessage{JSONRPC: "2.0", ID: json.RawMessage(`"server-1"`), Method: "example/call", Params: json.RawMessage(`{"value":7}`)})
		_ = conn.ReadJSON(&req)
		if string(req.ID) != `"server-1"` || string(req.Result) != `{"ok":true}` {
			t.Errorf("server response = id:%s result:%s", req.ID, req.Result)
		}
	})
	defer closeServer()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := Dial(ctx, endpoint, func(_ context.Context, req ServerRequest) (any, error) {
		if req.Method != "example/call" {
			t.Fatalf("method = %q", req.Method)
		}
		return map[string]bool{"ok": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_ = c.Close()
}

func rpcHandler(run func(*websocket.Conn)) http.Handler {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			run(conn)
		}
	})
}

func websocketServer(t *testing.T, run func(*websocket.Conn)) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: rpcHandler(run)}
	go server.Serve(listener)
	return "ws://" + listener.Addr().String() + "/rpc", func() { _ = server.Close() }
}
