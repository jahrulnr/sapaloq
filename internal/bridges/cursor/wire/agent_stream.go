// Package wire - Agent API raw HTTP/2 driver. Reuses the same h2 transport as
// StreamChatRaw (cursor-proto-lab compatible) but targets the Agent API RPC
// and uses the Agent protobuf envelope. This path is the same shape the
// reference 9router/open-sse/executors/cursorAgent.js uses; the JS reference
// calls it the "agentn.global.api5.cursor.sh" endpoint.
package wire

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/debug"
	"golang.org/x/net/http2"
)

// AgentStreamOptions configures the Agent API driver.
type AgentStreamOptions struct {
	Host    string // defaults to AgentAgentHost
	Path    string // defaults to AgentAgentPath
	Token   string
	MachineID string
	GhostMode bool
	Tools   []AgentTool
	Body    []byte // Connect-RPC framed AgentClientMessage
	Timeout time.Duration // 0 → 120s

	// MCPExecutor handles exec_mcp frames inside the api5 turn loop.
	MCPExecutor AgentMCPExecutor
	// OnMCPTool emits UI telemetry before MCPExecutor runs.
	OnMCPTool AgentMCPTelemetry

	// InsecureTLS controls TLS verification (for tests against self-signed
	// httptest servers). Production should leave this false.
	InsecureTLS bool
}

// AgentFrameHandler is invoked for each decoded AgentServerMessage.
type AgentFrameHandler func(decoded []AgentDecoded, raw []byte)

// StreamAgentHTTP2 drives the Agent API via net/http http2 with a duplex
// request body (pipe) so exec/KV responses can be written mid-stream.
func StreamAgentHTTP2(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
	if opts.InsecureTLS {
		return streamAgentHTTP2Simple(ctx, opts, onFrame)
	}
	return streamAgentBidirectionalHTTP2(ctx, opts, onFrame)
}

// StreamAgentRawWithRaw opens the raw HTTP/2 framer path. api5 currently
// rejects it with PROTOCOL_ERROR; kept for parity work against cursor-agent.
func StreamAgentRawWithRaw(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
	return streamAgentOverH2(ctx, opts, onFrame)
}

func streamAgentHTTP2Simple(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
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
	for k, v := range BuildAgentHeaders(opts.Token, opts.MachineID, opts.GhostMode) {
		req.Header.Set(k, v)
	}

	tlsCfg := &tls.Config{InsecureSkipVerify: opts.InsecureTLS}
	t2 := &http2.Transport{TLSClientConfig: tlsCfg}
	client := &http.Client{Transport: t2, Timeout: timeout}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent http status %d", resp.StatusCode)
	}

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
		_, payload, used, ok := ParseConnectFrame(buf[pos:end])
		if !ok {
			break
		}
		decoded := DecodeAgentServerMessage(payload)
		if onFrame != nil {
			onFrame(decoded, payload)
		}
		pos += used
	}
	return nil
}

func streamAgentBidirectionalHTTP2(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
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

	upload := newAgentUploadBody(opts.Body)
	state := &agentStreamState{
		ctx:         ctx,
		tools:       opts.Tools,
		blobStore:   map[string][]byte{},
		ackedExec:   map[string]struct{}{},
		mcpExecutor: opts.MCPExecutor,
		onMCPTool:   opts.OnMCPTool,
		onFrame:     onFrame,
		writeFrame:  upload.Write,
	}

	go func() {
		<-ctx.Done()
		_ = upload.Close()
	}()

	endpoint := "https://" + host + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, upload)
	if err != nil {
		_ = upload.Close()
		return err
	}
	for k, v := range BuildAgentHeaders(opts.Token, opts.MachineID, opts.GhostMode) {
		req.Header.Set(k, v)
	}

	tlsCfg := &tls.Config{
		ServerName:         urlHostOnly(host),
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: opts.InsecureTLS,
	}
	tr := &http2.Transport{
		TLSClientConfig:    tlsCfg,
		DisableCompression: true, // avoid extra accept-encoding; api5 auth-fingerprints the wire
	}

	resp, err := tr.RoundTrip(req)
	if err != nil {
		_ = upload.Close()
		return fmt.Errorf("agent h2 roundtrip: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = upload.Close()
		return fmt.Errorf("agent http status %d", resp.StatusCode)
	}

	var dataBuf bytes.Buffer
	readBuf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			_ = upload.Close()
			return err
		}
		n, err := resp.Body.Read(readBuf)
		if n > 0 {
			dataBuf.Write(readBuf[:n])
			if err := dispatchAgentFrames(&dataBuf, state); err != nil {
				_ = upload.Close()
				return err
			}
			if state.turnEnded {
				_ = upload.Close()
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if dataBuf.Len() > 0 {
					if err := dispatchAgentFrames(&dataBuf, state); err != nil {
						_ = upload.Close()
						return err
					}
				}
				_ = upload.Close()
				return nil
			}
			_ = upload.Close()
			return fmt.Errorf("read agent response: %w", err)
		}
	}
}

func streamAgentOverH2(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
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

	state := &agentStreamState{
		ctx:         ctx,
		tools:       opts.Tools,
		blobStore:   map[string][]byte{},
		ackedExec:   map[string]struct{}{},
		mcpExecutor: opts.MCPExecutor,
		onMCPTool:   opts.OnMCPTool,
		onFrame:     onFrame,
	}

	tlsCfg := &tls.Config{
		ServerName:         urlHostOnly(host),
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: opts.InsecureTLS,
		NextProtos:         []string{"h2"},
	}
	addr := host
	if !strings.Contains(addr, ":") {
		addr += ":443"
	}
	conn, err := dialHTTP2(ctx, addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("agent h2 connect: %w", err)
	}
	defer conn.Close()

	state.writeFrame = func(frame []byte) error {
		if len(frame) == 0 {
			return nil
		}
		return conn.framer.WriteData(1, false, frame)
	}

	headers := BuildAgentHeaders(opts.Token, opts.MachineID, opts.GhostMode)
	headers[":method"] = "POST"
	headers[":path"] = path
	headers[":scheme"] = "https"
	headers[":authority"] = urlHostOnly(host)

	return conn.sendAgentStream(ctx, headers, opts.Body, state)
}

// StreamAgentRawSimple is a one-shot agent stream using the raw framer (no exec
// loop). Useful for live smoke tests.
func StreamAgentRawSimple(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
	opts.MCPExecutor = nil
	opts.OnMCPTool = nil
	return streamAgentOverH2(ctx, opts, onFrame)
}

// SelectAgentStreamFn returns the configured Agent API stream driver based on
// SAPALOQ_AGENT_WIRE_DRIVER. Default: Node (9router http2 stack) when available,
// because api5 auth-fingerprints pure Go http2/raw clients with valid vscdb tokens.
func SelectAgentStreamFn() func(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("SAPALOQ_AGENT_WIRE_DRIVER")))
	switch driver {
	case "raw":
		return StreamAgentRawWithRaw
	case "http2":
		return StreamAgentHTTP2
	case "node":
		return StreamAgentNode
	default:
		if AgentNodeStreamAvailable() {
			return StreamAgentNode
		}
		return StreamAgentRawWithRaw
	}
}

// urlHostOnly strips an optional :port from a host:port pair. We avoid
// net.SplitHostPort because it rejects bare hostnames (no port).
func urlHostOnly(host string) string {
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}

type agentStreamState struct {
	ctx         context.Context
	tools       []AgentTool
	blobStore   map[string][]byte
	ackedExec   map[string]struct{}
	mcpExecutor AgentMCPExecutor
	onMCPTool   AgentMCPTelemetry
	turnEnded   bool
	onFrame     func(decoded []AgentDecoded, raw []byte)
	writeFrame  func(frame []byte) error
}

func (s *agentStreamState) processPayload(flags byte, payload []byte) error {
	if os.Getenv("SAPALOQ_AGENT_H2_DEBUG") == "1" {
		snippet := payload
		if len(snippet) > 48 {
			snippet = snippet[:48]
		}
		ev, evOK := DecodeExecServerEvent(payload)
		ctxID, ctxExec, ctxOK := DecodeAgentExecRequestContext(payload)
		debug.Debugf("agent frame flags=0x%02x len=%d hex=%s exec_ok=%v ctx_ok=%v ctx_id=%d",
			flags, len(payload), hex.EncodeToString(snippet), evOK, ctxOK, ctxID)
		if evOK {
			debug.Debugf("agent exec decode kind=%s id=%d exec=%s", ev.Kind, ev.ExecMsgID, ev.ExecID)
		}
		if ctxOK {
			debug.Debugf("agent exec context id=%d exec=%s", ctxID, ctxExec)
		}
	}

	// Exec handshake must be answered before the model streams; decode it before
	// treating the payload as a terminal Connect JSON error.
	if execEv, ok := DecodeExecServerEvent(payload); ok {
		debug.Debugf("agent exec event kind=%s id=%d exec=%s", execEv.Kind, execEv.ExecMsgID, execEv.ExecID)
		if err := s.handleExecEvent(s.ctx, execEv); err != nil {
			return err
		}
	} else if execMsgID, execID, ok := DecodeAgentExecRequestContext(payload); ok {
		debug.Debugf("agent exec context (fallback) id=%d exec=%s", execMsgID, execID)
		if err := s.handleExecEvent(s.ctx, ExecServerEvent{
			Kind: "exec_request_context", ExecMsgID: execMsgID, ExecID: execID,
		}); err != nil {
			return err
		}
	}

	if flags&ConnectFlagEndStream != 0 {
		if err := ParseConnectJSONError(payload); err != nil {
			return err
		}
	} else if len(payload) > 0 && payload[0] == '{' {
		if err := ParseConnectJSONError(payload); err != nil {
			return err
		}
	}
	if kv, ok := DecodeKvServerEvent(payload); ok {
		switch kv.Kind {
		case "kv_get_blob":
			blobKey := hex.EncodeToString(kv.BlobID)
			if err := s.writeFrame(BuildKvGetBlobResult(kv.KvID, s.blobStore[blobKey], kv.RequestMetadata)); err != nil {
				return err
			}
		case "kv_set_blob":
			blobKey := hex.EncodeToString(kv.BlobID)
			s.blobStore[blobKey] = append([]byte(nil), kv.BlobData...)
			if err := s.writeFrame(BuildKvSetBlobResult(kv.KvID, kv.RequestMetadata)); err != nil {
				return err
			}
		}
	}
	decoded := DecodeAgentServerMessage(payload)
	for _, d := range decoded {
		if d.Kind == "turn_ended" {
			s.turnEnded = true
		}
	}
	if s.onFrame != nil {
		s.onFrame(decoded, payload)
	}
	return nil
}

func dispatchAgentFrames(buf *bytes.Buffer, state *agentStreamState) error {
	raw := buf.Bytes()
	consumed := 0
	for consumed < len(raw) {
		flags, payload, used, ok := ParseConnectFrame(raw[consumed:])
		if !ok {
			break
		}
		consumed += used
		if err := state.processPayload(flags, payload); err != nil {
			return err
		}
		if state.turnEnded {
			buf.Next(consumed)
			return nil
		}
	}
	if consumed > 0 {
		buf.Next(consumed)
	}
	return nil
}

// keep http imported for the http2 fallback transport setup
var _ = http.NoBody
