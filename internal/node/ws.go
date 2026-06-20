package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// WSTransport is a WebSocket client Transport for remote nodes. The node URL
// (wss://… or ws://…) comes from the node's address/comm-spec; auth tokens are
// supplied via an Authorization header sourced from an ENV var by the caller
// (never persisted in the nodes table).
type WSTransport struct {
	url     string
	headers http.Header

	mu   sync.Mutex
	conn *websocket.Conn
}

// NewWS dials the node URL. A dial/handshake failure returns an error so the
// orchestrator can fall back to local-default when allowed. authToken may be
// empty; when set it is sent as a Bearer Authorization header.
func NewWS(ctx context.Context, url, authToken string) (*WSTransport, error) {
	if url == "" {
		return nil, fmt.Errorf("node: websocket url is required")
	}
	headers := http.Header{}
	if authToken != "" {
		headers.Set("Authorization", "Bearer "+authToken)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, headers)
	if err != nil {
		return nil, fmt.Errorf("node: ws dial %s: %w", url, err)
	}
	return &WSTransport{url: url, headers: headers, conn: conn}, nil
}

// Spawn sends the envelope and streams progress frames until the socket closes
// or a frame has Done=true.
func (w *WSTransport) Spawn(ctx context.Context, env SpawnEnvelope) (<-chan Progress, error) {
	env = EnforceRemoteInvariants(env)
	w.mu.Lock()
	conn := w.conn
	w.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("node: transport closed")
	}
	frame, err := json.Marshal(map[string]any{"op": "spawn", "envelope": env})
	if err != nil {
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		return nil, fmt.Errorf("node: ws write: %w", err)
	}
	out := make(chan Progress, 16)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, data, err := conn.ReadMessage()
			if err != nil {
				select {
				case out <- Progress{Kind: "error", Error: err.Error(), Done: true}:
				default:
				}
				return
			}
			var p Progress
			if err := json.Unmarshal(data, &p); err != nil {
				continue
			}
			select {
			case out <- p:
			case <-ctx.Done():
				return
			}
			if p.Done {
				return
			}
		}
	}()
	return out, nil
}

// Control sends a lifecycle action frame.
func (w *WSTransport) Control(ctx context.Context, subAgentID, action string) error {
	w.mu.Lock()
	conn := w.conn
	w.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("node: transport closed")
	}
	frame, _ := json.Marshal(map[string]any{"op": "control", "sub_agent_id": subAgentID, "action": action})
	return conn.WriteMessage(websocket.TextMessage, frame)
}

// Close closes the underlying socket.
func (w *WSTransport) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn == nil {
		return nil
	}
	err := w.conn.Close()
	w.conn = nil
	return err
}

var _ Transport = (*WSTransport)(nil)
