// Package wire - Agent API raw HTTP/2 driver. Reuses the same h2 transport as
// StreamChatRaw (cursor-proto-lab compatible) but targets the Agent API RPC
// and uses the Agent protobuf envelope. This path is the same shape the
// reference 9router/open-sse/executors/cursorAgent.js uses; the JS reference
// calls it the "agentn.global.api5.cursor.sh" endpoint.
package wire

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// AgentStreamOptions configures the Agent API driver.
type AgentStreamOptions struct {
	Host    string // defaults to AgentAgentHost
	Path    string // defaults to AgentAgentPath
	Token   string
	Body    []byte        // Connect-RPC framed AgentClientMessage
	Timeout time.Duration // 0 → 120s

	// InsecureTLS controls TLS verification (for tests against self-signed
	// httptest servers). Production should leave this false.
	InsecureTLS bool
}

// AgentFrameHandler is invoked for each decoded AgentServerMessage.
type AgentFrameHandler func(decoded []AgentDecoded, raw []byte)

// StreamAgentRawWithRaw opens a raw HTTP/2 connection (Node http2 compatible)
// and sends one client message. The post-decompress frame bytes are passed
// alongside the decoded AgentServerMessage events.
//
// Mirrors 9router/open-sse/executors/cursorAgent.js. Reuses h2Conn from
// raw.go so it shares the same Node-style preface/empty-SETTINGS behaviour.
func StreamAgentRawWithRaw(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
	if opts.Token == "" {
		return fmt.Errorf("cursor token is required for agent stream")
	}
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		host = AgentAgentHost
	}
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		path = AgentAgentPath
	}
	addr := host
	if !strings.Contains(addr, ":") {
		addr += ":443"
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tlsCfg := &tls.Config{
		ServerName:         urlHostOnly(host),
		MinVersion:         tls.VersionTLS12,
		NextProtos:         []string{"h2"},
		InsecureSkipVerify: opts.InsecureTLS,
	}

	conn, err := dialHTTP2(ctx, addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("agent h2 connect: %w", err)
	}
	defer conn.Close()

	headers := map[string]string{
		":method":       "POST",
		":path":         path,
		":scheme":       "https",
		":authority":    host,
		"content-type":  "application/connect+proto",
		"authorization": "Bearer " + opts.Token,
	}

	return conn.sendAndRecvRaw(ctx, headers, opts.Body, func(payload []byte) {
		decoded := DecodeAgentServerMessage(payload)
		if onFrame != nil {
			onFrame(decoded, payload)
		}
	})
}

// StreamAgentHTTP2 is a stdlib-based fallback that drives the Agent API via
// Go's net/http + http2.Transport. Used by tests (httptest's h2 server has
// incompatibilities with our raw framer driver) and as a fallback when
// SAPALOQ_AGENT_WIRE_DRIVER=http2 is set.
func StreamAgentHTTP2(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
	if opts.Token == "" {
		return fmt.Errorf("cursor token is required for agent stream")
	}
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		host = AgentAgentHost
	}
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		path = AgentAgentPath
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoint := "https://" + host + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(opts.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/connect+proto")
	req.Header.Set("Authorization", "Bearer "+opts.Token)

	tlsCfg := &tls.Config{InsecureSkipVerify: opts.InsecureTLS}
	t2 := &http2.Transport{
		TLSClientConfig: tlsCfg,
	}
	client := &http.Client{Transport: t2, Timeout: timeout}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent http status %d", resp.StatusCode)
	}

	// Walk Connect-RPC frames in the response body. Each frame is 5 bytes
	// of prefix + payload. Run in a goroutine-friendly loop.
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	for pos := 0; pos+5 <= len(buf); {
		length := uint32(buf[pos+1])<<24 | uint32(buf[pos+2])<<16 | uint32(buf[pos+3])<<8 | uint32(buf[pos+4])
		end := pos + 5 + int(length)
		if end > len(buf) {
			break
		}
		payload := decompressAgentFrame(buf[pos+5 : end])
		decoded := DecodeAgentServerMessage(payload)
		if onFrame != nil {
			onFrame(decoded, payload)
		}
		pos = end
	}
	return nil
}

// SelectAgentStreamFn returns the configured Agent API stream driver based on
// the SAPALOQ_AGENT_WIRE_DRIVER env var. Default: StreamAgentRawWithRaw.
func SelectAgentStreamFn() func(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SAPALOQ_AGENT_WIRE_DRIVER")), "http2") {
		return StreamAgentHTTP2
	}
	return StreamAgentRawWithRaw
}

// urlHostOnly strips an optional :port from a host:port pair. We avoid
// net.SplitHostPort because it rejects bare hostnames (no port).
func urlHostOnly(host string) string {
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}

// sendAndRecvRaw is the Agent-specific raw-frame variant of h2Conn.sendAndRecv.
// It exposes the raw post-decompress Connect-RPC frame bytes so callers can
// drive the ExecServerMessage handshake (request-context ack, KV blob
// replies, tool rejection, etc.).
func (c *h2Conn) sendAndRecvRaw(ctx context.Context, headers map[string]string, body []byte, onFrame func(payload []byte)) error {
	const streamID = 1

	c.hbuf.Reset()
	enc := hpack.NewEncoder(&c.hbuf)
	for k, v := range headers {
		if err := enc.WriteField(hpack.HeaderField{Name: k, Value: v}); err != nil {
			return fmt.Errorf("hpack %s: %w", k, err)
		}
	}
	if err := c.framer.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      streamID,
		BlockFragment: c.hbuf.Bytes(),
		EndStream:     false,
		EndHeaders:    true,
	}); err != nil {
		return fmt.Errorf("write headers: %w", err)
	}
	if err := c.framer.WriteWindowUpdate(streamID, 1<<24); err != nil {
		return fmt.Errorf("write window update: %w", err)
	}
	if err := c.framer.WriteData(streamID, true, body); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := c.framer.ReadFrame()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read frame: %w", err)
		}
		switch v := f.(type) {
		case *http2.MetaHeadersFrame:
			for _, f := range v.Fields {
				_ = f.Name
				_ = f.Value
			}
		case *http2.DataFrame:
			onFrame(decompressAgentFrame(v.Data()))
		case *http2.GoAwayFrame:
			return fmt.Errorf("goaway: %s", v.DebugData())
		case *http2.RSTStreamFrame:
			return fmt.Errorf("rst_stream code=%d", v.ErrCode)
		case *http2.WindowUpdateFrame:
			// flow control - non-essential for a single request
		case *http2.SettingsFrame:
			// server settings; no ack needed unless requested
		}
	}
}

// decompressAgentFrame handles the Connect-RPC gzip envelope (FLAG_GZIP =
// 0x01). Mirrors iterateConnectFrames' per-frame prefix logic.
func decompressAgentFrame(buf []byte) []byte {
	if len(buf) < 5 {
		return buf
	}
	flags := buf[0]
	length := uint32(buf[1])<<24 | uint32(buf[2])<<16 | uint32(buf[3])<<8 | uint32(buf[4])
	if int(length) > len(buf)-5 {
		return buf
	}
	raw := buf[5 : 5+length]
	if flags&0x01 == 0 {
		return raw
	}
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return raw
	}
	defer gr.Close()
	expanded, err := io.ReadAll(gr)
	if err != nil {
		return raw
	}
	return expanded
}

// keep url imported for the address parser above
var _ = url.JoinPath

// keep http imported for the http2 fallback transport setup
var _ = http.NoBody
