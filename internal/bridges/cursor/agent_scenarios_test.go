package cursor

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
	"golang.org/x/net/http2"
)

// TestAgentPathMockUnauthenticated covers the failure path: server returns
// HTTP 401 with a JSON error body. We assert EventError fires (mirrors
// live-unauthenticated on the chat path).
func TestAgentPathMockUnauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(wire.AgentAgentPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthenticated","message":"User not authenticated"}}`))
	})
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	prev := snapshotCredentialEnv()
	t.Cleanup(func() { restoreCredentialEnv(t, prev) })
	t.Setenv("SAPALOQ_CURSOR_TOKEN", "agent-token")
	t.Setenv("CURSOR_ACCESS_TOKEN", "agent-token")
	t.Setenv("CURSOR_MACHINE_ID", "agent-machine")
	t.Setenv("CURSOR_STATE_VSCDB", filepath.Join(t.TempDir(), "missing.vscdb"))
	t.Setenv("SAPALOQ_WIRE_INSECURE_TLS", "1")
	t.Setenv("SAPALOQ_AGENT_PATH", "1")
	t.Setenv("SAPALOQ_AGENT_WIRE_DRIVER", "http2")
	t.Setenv("CURSOR_AGENT_HOST", strings.TrimPrefix(server.URL, "https://"))
	t.Setenv("CURSOR_AGENT_PATH", wire.AgentAgentPath)

	entry, runtime := defaultTestEntry()
	b, err := New(entry, runtime)
	if err != nil {
		t.Fatal(err)
	}
	events := collectBridgeEvents(t, b, "hello")
	sawError := false
	for _, ev := range events {
		if ev.Kind == bridge.EventError {
			sawError = true
			if ev.Error == "" {
				t.Fatalf("EventError with empty Error")
			}
		}
	}
	if !sawError {
		t.Fatalf("expected EventError for unauthenticated, got %+v", events)
	}
}

// TestAgentWireContractSmoke verifies the Agent API request body builder
// produces a syntactically valid Connect-RPC frame and that the server can
// read it (via stdlib http2 client to an httptest mock).
func TestAgentWireContractSmoke(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(wire.AgentAgentPath, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		t.Logf("server received bytes=%d", len(body))
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(http.StatusOK)
		payload := wire.BuildSyntheticAgentText("hi from agent")
		_, _ = w.Write(wire.WrapConnectFrame(payload, false))
	})
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	body := wire.BuildAgentRequestBody(wire.AgentRunOptions{
		UserText:       "describe this",
		ModelID:        "claude-4.6-sonnet-medium",
		ConversationID: "scenario",
	})
	client := &http.Client{Transport: newH2TestTransport()}
	req, _ := http.NewRequest("POST", server.URL+wire.AgentAgentPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/connect+proto")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	decoded := wire.DecodeAgentServerMessage(decodeOneAgentFrame(respBody))
	if len(decoded) != 1 || decoded[0].Kind != "text" || decoded[0].Text != "hi from agent" {
		t.Fatalf("decoded = %+v, want [{Kind:text Text:hi from agent}]", decoded)
	}
}

// TestAgentVisionAutoRouteInBridge verifies that a message containing
// `data:image/...` auto-routes through the Agent API path without requiring
// SAPALOQ_AGENT_PATH=1. This is the second half of "point 2" (vision
// auto-detection).
func TestAgentVisionAutoRouteInBridge(t *testing.T) {
	mux := http.NewServeMux()
	hitCh := make(chan struct{}, 1)
	mux.HandleFunc(wire.AgentAgentPath, func(w http.ResponseWriter, r *http.Request) {
		select {
		case hitCh <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(http.StatusOK)
		payload := wire.BuildSyntheticAgentText("ok")
		_, _ = w.Write(wire.WrapConnectFrame(payload, false))
	})
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	prev := snapshotCredentialEnv()
	t.Cleanup(func() { restoreCredentialEnv(t, prev) })
	t.Setenv("SAPALOQ_CURSOR_TOKEN", "agent-token")
	t.Setenv("CURSOR_ACCESS_TOKEN", "agent-token")
	t.Setenv("CURSOR_MACHINE_ID", "agent-machine")
	t.Setenv("CURSOR_STATE_VSCDB", filepath.Join(t.TempDir(), "missing.vscdb"))
	t.Setenv("SAPALOQ_WIRE_INSECURE_TLS", "1")
	t.Setenv("SAPALOQ_AGENT_WIRE_DRIVER", "http2")
	// Deliberately NOT setting SAPALOQ_AGENT_PATH - vision content should
	// auto-route.
	t.Setenv("CURSOR_AGENT_HOST", strings.TrimPrefix(server.URL, "https://"))
	t.Setenv("CURSOR_AGENT_PATH", wire.AgentAgentPath)

	entry, runtime := defaultTestEntry()
	b, err := New(entry, runtime)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := b.Complete(context.Background(), bridge.Request{
		SessionID: "vision-1",
		Messages: []bridge.Message{{
			Role:    "user",
			Content: "what is this? data:image/png;base64,iVBORw0KGgo=",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}
	select {
	case <-hitCh:
		// expected - Agent API endpoint was hit
	default:
		t.Fatalf("expected Agent API endpoint to be called (vision routing failed)")
	}
}

// newH2TestTransport returns an http2.Transport that accepts any TLS cert.
// Used by tests talking to httptest servers; production uses the raw framer
// driver (StreamAgentRawWithRaw) for byte-perfect Node http2 compatibility.
func newH2TestTransport() http.RoundTripper {
	tlsCfg := &tls.Config{InsecureSkipVerify: true}
	return &http2.Transport{
		TLSClientConfig: tlsCfg,
		DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
			return tls.Dial(network, addr, cfg)
		},
	}
}

// decodeOneAgentFrame extracts the first Connect-RPC frame payload from a
// stream response body (5-byte prefix + protobuf payload).
func decodeOneAgentFrame(buf []byte) []byte {
	if len(buf) < 5 {
		return buf
	}
	length := uint32(buf[1])<<24 | uint32(buf[2])<<16 | uint32(buf[3])<<8 | uint32(buf[4])
	if int(length) > len(buf)-5 {
		return buf[5:]
	}
	return buf[5 : 5+length]
}
