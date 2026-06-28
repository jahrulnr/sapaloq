// Package appserver implements the Codex app-server JSON-RPC transport.
// Both TCP and Unix endpoints carry one JSON-RPC message per WebSocket text
// frame. Unix sockets still perform the standard HTTP Upgrade handshake.
package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const maxMessageBytes = 128 << 20

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("JSON-RPC %d: %s", e.Code, e.Message)
}

type Notification struct {
	Method string
	Params json.RawMessage
}

type ServerRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

type ServerRequestHandler func(context.Context, ServerRequest) (any, error)

type response struct {
	result json.RawMessage
	err    error
}

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type Client struct {
	conn *websocket.Conn

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan response
	nextID  atomic.Int64

	notifications chan Notification
	done          chan struct{}
	closeOnce     sync.Once
	readErr       atomic.Value

	handler ServerRequestHandler
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func Dial(ctx context.Context, endpoint string, handler ServerRequestHandler) (*Client, error) {
	conn, err := dialWebSocket(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxMessageBytes)
	clientCtx, cancel := context.WithCancel(context.Background())
	c := &Client{
		conn:          conn,
		pending:       make(map[string]chan response),
		notifications: make(chan Notification, 256),
		done:          make(chan struct{}),
		handler:       handler,
		ctx:           clientCtx,
		cancel:        cancel,
	}
	c.wg.Add(1)
	go c.readLoop()
	return c, nil
}

func dialWebSocket(ctx context.Context, endpoint string) (*websocket.Conn, error) {
	endpoint = strings.TrimSpace(endpoint)
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	url := endpoint
	if strings.HasPrefix(endpoint, "unix://") {
		path := strings.TrimPrefix(endpoint, "unix://")
		if path == "" {
			return nil, errors.New("codex app-server: unix endpoint has no socket path")
		}
		dialer.NetDialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}
		url = "ws://localhost/rpc"
	}
	if !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
		return nil, fmt.Errorf("codex app-server: unsupported endpoint %q", endpoint)
	}
	conn, resp, err := dialer.DialContext(ctx, url, http.Header{})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("codex app-server: websocket upgrade %s: %w", resp.Status, err)
		}
		return nil, fmt.Errorf("codex app-server: connect %s: %w", endpoint, err)
	}
	return conn, nil
}

func (c *Client) Initialize(ctx context.Context) error {
	params := map[string]any{
		"clientInfo": map[string]any{
			"name": "sapaloq", "title": "SapaLOQ", "version": "1",
		},
		"capabilities": map[string]any{"experimentalApi": true},
	}
	if err := c.Call(ctx, "initialize", params, nil); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := c.Notify("initialized", nil); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}
	return nil
}

func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	idRaw := json.RawMessage(fmt.Sprintf("%d", id))
	key := string(idRaw)
	ch := make(chan response, 1)
	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()

	msg := wireMessage{JSONRPC: "2.0", ID: idRaw, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			c.removePending(key)
			return err
		}
		msg.Params = b
	}
	if err := c.writeJSON(msg); err != nil {
		c.removePending(key)
		return err
	}

	select {
	case <-ctx.Done():
		c.removePending(key)
		return ctx.Err()
	case <-c.done:
		return c.connectionError()
	case res := <-ch:
		if res.err != nil {
			return res.err
		}
		if out == nil || len(res.result) == 0 || string(res.result) == "null" {
			return nil
		}
		return json.Unmarshal(res.result, out)
	}
}

func (c *Client) Notify(method string, params any) error {
	msg := wireMessage{JSONRPC: "2.0", Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		msg.Params = b
	}
	return c.writeJSON(msg)
}

func (c *Client) Notifications() <-chan Notification { return c.notifications }
func (c *Client) Done() <-chan struct{}              { return c.done }

func (c *Client) Err() error {
	select {
	case <-c.done:
		return c.connectionError()
	default:
		return nil
	}
}

func (c *Client) Close() error {
	c.cancel()
	err := c.conn.Close()
	c.wg.Wait()
	return err
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	defer c.finish(errors.New("codex app-server connection closed"))
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.finish(err)
			return
		}
		var msg wireMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			request := ServerRequest{ID: cloneRaw(msg.ID), Method: msg.Method, Params: cloneRaw(msg.Params)}
			c.wg.Add(1)
			go func() {
				defer c.wg.Done()
				c.handleServerRequest(request)
			}()
		case msg.Method != "":
			select {
			case c.notifications <- Notification{Method: msg.Method, Params: cloneRaw(msg.Params)}:
			case <-c.ctx.Done():
				return
			}
		case len(msg.ID) > 0:
			key := string(msg.ID)
			c.mu.Lock()
			ch := c.pending[key]
			delete(c.pending, key)
			c.mu.Unlock()
			if ch != nil {
				if msg.Error != nil {
					ch <- response{err: msg.Error}
				} else {
					ch <- response{result: cloneRaw(msg.Result)}
				}
			}
		}
	}
}

func (c *Client) handleServerRequest(req ServerRequest) {
	var result any
	var err error
	if c.handler == nil {
		err = fmt.Errorf("unsupported server request %s", req.Method)
	} else {
		result, err = c.handler(c.ctx, req)
	}
	msg := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if err != nil {
		msg["error"] = map[string]any{"code": -32603, "message": err.Error()}
	} else {
		msg["result"] = result
	}
	_ = c.writeJSON(msg)
}

func (c *Client) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	return c.conn.WriteJSON(v)
}

func (c *Client) finish(err error) {
	c.closeOnce.Do(func() {
		if err != nil {
			c.readErr.Store(err)
		}
		c.cancel()
		close(c.done)
		close(c.notifications)
		c.mu.Lock()
		for key, ch := range c.pending {
			delete(c.pending, key)
			ch <- response{err: err}
		}
		c.mu.Unlock()
		_ = c.conn.Close()
	})
}

func (c *Client) removePending(key string) {
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
}

func (c *Client) connectionError() error {
	if v := c.readErr.Load(); v != nil {
		return v.(error)
	}
	return errors.New("codex app-server connection closed")
}

func cloneRaw(in json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), in...)
}
