// Package wire - raw HTTP/2 client that mirrors Node.js `http2.connect()` byte
// for byte. Cursor's api2 rejects Go's net/http http2 client with "User is
// unauthorized" even with identical headers; the difference is in how Go and
// Node serialise frames. Sending the frames directly with http2.Framer matches
// what cursor-bridge ships.
package wire

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// StreamChatRaw is the cursor-bridge-compatible stream driver. It opens a
// fresh HTTP/2 connection, sends one request, and emits frames exactly the way
// Node's http2.connect does.
func StreamChatRaw(ctx context.Context, opts StreamOptions, onFrame FrameHandler) error {
	if opts.Token == "" {
		return fmt.Errorf("cursor token is required for live stream")
	}
	endpoint := strings.TrimRight(opts.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://api2.cursor.sh"
	}
	if !strings.Contains(endpoint, "StreamUnifiedChatWithTools") {
		endpoint += defaultChatPath
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}
	host := u.Host
	if u.Port() == "" {
		host += ":443"
	}

	body := BuildChatBody(opts.Messages, opts.Model)
	headers := BuildHeaders(opts.Token, opts.MachineID, opts.GhostMode)
	headers[":method"] = "POST"
	headers[":path"] = u.Path
	headers[":scheme"] = "https"
	headers[":authority"] = u.Host

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tlsCfg := &tls.Config{
		ServerName:         u.Hostname(),
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: opts.InsecureTLS,
		NextProtos:         []string{"h2"},
	}

	conn, err := dialHTTP2(ctx, host, tlsCfg)
	if err != nil {
		return fmt.Errorf("h2 connect: %w", err)
	}
	defer conn.Close()

	return conn.sendAndRecv(ctx, headers, body, onFrame)
}

// h2Conn wraps a TLS+HTTP/2 connection speaking the cursor-proto-lab wire.
type h2Conn struct {
	tlsConn net.Conn
	framer  *http2.Framer
	hbuf    bytes.Buffer
}

// debugConnLog is a stub kept here so the test code can swap it for a real
// logger without rebuilding production callers. Default is no-op.
var debugConnLog = func(string, ...any) {}

// EnableDebugConnLog turns on connection-level frame logging. Tests set this
// to capture raw http2 frame traffic for diagnostics.
func EnableDebugConnLog() {
	debugConnLog = func(format string, args ...any) {
		// Avoid pulling fmt into hot path; use log via stderr directly.
		// (Keep small to avoid recompilation churn.)
		//nolint:errcheck
		_, _ = fmt.Fprintf(os.Stderr, "[h2] "+format+"\n", args...)
	}
}

func dialHTTP2(ctx context.Context, addr string, tlsCfg *tls.Config) (*h2Conn, error) {
	d := net.Dialer{KeepAlive: 30 * time.Second}
	if deadline, ok := ctx.Deadline(); ok {
		d.Deadline = deadline
	}
	rawConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	tlsConn := tls.Client(rawConn, tlsCfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}
	if got := tlsConn.ConnectionState().NegotiatedProtocol; got != "h2" {
		tlsConn.Close()
		return nil, fmt.Errorf("alpn did not negotiate h2 (got %q)", got)
	}

	br := bufio.NewReader(tlsConn)
	framer := http2.NewFramer(tlsConn, br)
	framer.AllowIllegalReads = true
	framer.AllowIllegalWrites = true
	// We don't pre-set ReadMetaHeaders here; reads use the default
	// hpack decoder per-frame. Pre-setting causes the framer to expect
	// every HEADERS frame to be HPACK-decoded, which trips up the
	// first frame round-trip with some h2 servers (httptest).
	preface := []byte(http2.ClientPreface)
	// Send preface + client SETTINGS together. Node http2 sends a
	// non-empty SETTINGS at startup - MAX_CONCURRENT_STREAMS,
	// INITIAL_WINDOW_SIZE, MAX_FRAME_SIZE - and we mirror that to keep
	// servers (including Go's httptest) happy.
	if _, err := tlsConn.Write(preface); err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("write preface: %w", err)
	}
	if err := framer.WriteSettings(
		http2.Setting{ID: http2.SettingMaxConcurrentStreams, Val: 100},
		http2.Setting{ID: http2.SettingInitialWindowSize, Val: 65535},
		http2.Setting{ID: http2.SettingMaxFrameSize, Val: 16384},
	); err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("write settings: %w", err)
	}

	debugConnLog("client preface sent, settings written")
	// Read frames until we see SETTINGS (server's startup frame). Other
	// frames (WINDOW_UPDATE, SETTINGS+ACK, PING) are tolerated but must be
	// drained before we send our request.
	for {
		f, err := framer.ReadFrame()
		if err != nil {
			tlsConn.Close()
			return nil, fmt.Errorf("read server settings: %w", err)
		}
		if sf, ok := f.(*http2.SettingsFrame); ok && !sf.IsAck() {
			break
		}
	}
	if err := framer.WriteSettingsAck(); err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("write settings ack: %w", err)
	}

	return &h2Conn{
		tlsConn: tlsConn,
		framer:  framer,
	}, nil
}

// AgentStreamFn is the function type used by the bridge to drive the Agent
// API path. Tests can substitute a different transport to avoid httptest
// incompatibilities; production defaults to StreamAgentRawWithRaw.
type AgentStreamFn func(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error

func (c *h2Conn) Close() error { return c.tlsConn.Close() }

func (c *h2Conn) sendAndRecv(ctx context.Context, headers map[string]string, body []byte, onFrame FrameHandler) error {
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
		EndHeaders:    true,
		EndStream:     false,
	}); err != nil {
		return fmt.Errorf("write headers: %w", err)
	}
	debugConnLog("wrote headers len=%d", len(c.hbuf.Bytes()))
	// Bump the stream-level window up to match the connection window so the
	// server's response stream isn't blocked on flow control. Without this,
	// some servers wait for WINDOW_UPDATE before they start emitting frames
	// (matches Node http2 default behaviour).
	if err := c.framer.WriteWindowUpdate(streamID, 1<<24); err != nil {
		return fmt.Errorf("write window update: %w", err)
	}
	if err := c.framer.WriteData(streamID, true, body); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	var dataBuf bytes.Buffer
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := c.framer.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("read frame: %w", err)
		}
		switch fr := f.(type) {
		case *http2.MetaHeadersFrame:
			for _, hf := range fr.Fields {
				if hf.Name == ":status" && hf.Value != "200" {
					return fmt.Errorf("cursor api status %s", hf.Value)
				}
			}
			if fr.StreamEnded() {
				if dataBuf.Len() > 0 {
					dispatchRawFrames(dataBuf.Bytes(), onFrame)
				}
				return nil
			}
		case *http2.DataFrame:
			if fr.StreamID != streamID {
				continue
			}
			if n := len(fr.Data()); n > 0 {
				dataBuf.Write(fr.Data())
			}
			if fr.StreamEnded() {
				dispatchRawFrames(dataBuf.Bytes(), onFrame)
				return nil
			}
		case *http2.WindowUpdateFrame, *http2.PingFrame, *http2.SettingsFrame:
			// ignore
		case *http2.RSTStreamFrame:
			return fmt.Errorf("cursor api reset stream %d: %s", fr.StreamID, fr.ErrCode)
		case *http2.GoAwayFrame:
			return fmt.Errorf("cursor api goaway: %s", fr.ErrCode)
		}
	}
	if dataBuf.Len() > 0 {
		dispatchRawFrames(dataBuf.Bytes(), onFrame)
	}
	return nil
}

func dispatchRawFrames(raw []byte, onFrame FrameHandler) {
	consumed := 0
	for consumed < len(raw) {
		flags, payload, used, ok := ParseConnectFrame(raw[consumed:])
		if !ok {
			break
		}
		consumed += used
		_ = DispatchConnectPayload(flags, payload, onFrame)
	}
}

// emptySettingsFrame is the SETTINGS frame (length 0, type 4, flags 0, stream 0).
// Mirrors what Node http2 sends on connection startup.
var emptySettingsFrame = func() []byte {
	var b bytes.Buffer
	b.WriteByte(0)
	b.WriteByte(0)
	b.WriteByte(0)
	b.WriteByte(0)
	b.WriteByte(0x4)
	b.WriteByte(0)
	binary.Write(&b, binary.BigEndian, uint32(0))
	return b.Bytes()
}()
