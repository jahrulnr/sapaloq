package wire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type agentH2GatewayConfig struct {
	Host      string            `json:"host"`
	Path      string            `json:"path"`
	Headers   map[string]string `json:"headers"`
	BodyB64   string            `json:"bodyB64"`
	TimeoutMs int               `json:"timeoutMs,omitempty"`
}

type agentH2GatewayMsg struct {
	T    string `json:"t"`
	B64  string `json:"b64,omitempty"`
	Code int    `json:"code,omitempty"`
	Msg  string `json:"msg,omitempty"`
}

type agentH2GatewayConn struct {
	mu     sync.Mutex
	stdin  io.WriteCloser
	closed bool
}

func (g *agentH2GatewayConn) writeLine(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return io.ErrClosedPipe
	}
	_, err = g.stdin.Write(append(line, '\n'))
	return err
}

func (g *agentH2GatewayConn) WriteFrame(frame []byte) error {
	if len(frame) == 0 {
		return nil
	}
	return g.writeLine(agentH2GatewayMsg{T: "write", B64: base64.StdEncoding.EncodeToString(frame)})
}

func (g *agentH2GatewayConn) CloseUpload() error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	stdin := g.stdin
	g.mu.Unlock()
	line, err := json.Marshal(agentH2GatewayMsg{T: "close"})
	if err != nil {
		return stdin.Close()
	}
	_, _ = stdin.Write(append(line, '\n'))
	return stdin.Close()
}

// StreamAgentNode drives api5 through a thin Node HTTP/2 gateway. All protobuf,
// exec/MCP handling, and headers are owned by Go (agentStreamState).
func StreamAgentNode(ctx context.Context, opts AgentStreamOptions, onFrame func(decoded []AgentDecoded, raw []byte)) error {
	if opts.Token == "" {
		return fmt.Errorf("cursor token is required for agent stream")
	}
	script, err := agentH2GatewayScriptPath()
	if err != nil {
		return err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("node not found for agent h2 gateway: %w", err)
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
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	headers := BuildAgentHeaders(opts.Token, opts.MachineID, opts.GhostMode)
	headers[":method"] = "POST"
	headers[":path"] = path
	headers[":scheme"] = "https"
	headers[":authority"] = urlHostOnly(host)

	cfg := agentH2GatewayConfig{
		Host:      urlHostOnly(host),
		Path:      path,
		Headers:   headers,
		BodyB64:   base64.StdEncoding.EncodeToString(opts.Body),
		TimeoutMs: int(timeout / time.Millisecond),
	}
	cfgLine, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(runCtx, node, script)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("agent h2 gateway start: %w", err)
	}

	gw := &agentH2GatewayConn{stdin: stdin}
	state := &agentStreamState{
		ctx:         runCtx,
		tools:       opts.Tools,
		blobStore:   map[string][]byte{},
		ackedExec:   map[string]struct{}{},
		mcpExecutor: opts.MCPExecutor,
		onMCPTool:   opts.OnMCPTool,
		onFrame:     onFrame,
		writeFrame:  gw.WriteFrame,
	}

	go func() {
		<-runCtx.Done()
		_ = gw.CloseUpload()
	}()

	if _, err := stdin.Write(append(cfgLine, '\n')); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("agent h2 gateway config: %w", err)
	}

	var dataBuf bytes.Buffer
	var streamErr error
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		if err := runCtx.Err(); err != nil {
			streamErr = err
			break
		}
		var msg agentH2GatewayMsg
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			streamErr = fmt.Errorf("agent h2 gateway json: %w", err)
			break
		}
		switch msg.T {
		case "status":
			if msg.Code != 0 && msg.Code != 200 {
				streamErr = fmt.Errorf("agent http status %d", msg.Code)
			}
		case "data":
			if msg.B64 == "" {
				continue
			}
			chunk, err := base64.StdEncoding.DecodeString(msg.B64)
			if err != nil {
				streamErr = fmt.Errorf("agent h2 gateway data b64: %w", err)
				break
			}
			if len(chunk) == 0 {
				continue
			}
			dataBuf.Write(chunk)
			if err := dispatchAgentFrames(&dataBuf, state); err != nil {
				streamErr = err
			}
			if state.turnEnded {
				_ = gw.CloseUpload()
				goto done
			}
		case "err":
			if msg.Msg != "" {
				streamErr = fmt.Errorf("cursor api error %s: %s", classifyAgentAPIError(msg.Msg), msg.Msg)
			} else {
				streamErr = fmt.Errorf("cursor agent h2 gateway failed")
			}
			goto done
		case "end":
			goto done
		}
		if streamErr != nil {
			break
		}
	}
done:
	if streamErr == nil {
		if err := scanner.Err(); err != nil {
			streamErr = fmt.Errorf("agent h2 gateway read: %w", err)
		} else if dataBuf.Len() > 0 {
			if err := dispatchAgentFrames(&dataBuf, state); err != nil {
				streamErr = err
			}
		}
	}

	_ = gw.CloseUpload()
	waitErr := cmd.Wait()
	if streamErr != nil {
		return streamErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return fmt.Errorf("cursor agent h2 gateway failed: %s", msg)
	}
	return nil
}

func agentH2GatewayScriptPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("SAPALOQ_AGENT_H2_GATEWAY")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("SAPALOQ_AGENT_H2_GATEWAY not found: %s", p)
	}
	if p := strings.TrimSpace(os.Getenv("SAPALOQ_AGENT_NODE_SCRIPT")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("SAPALOQ_AGENT_NODE_SCRIPT not found: %s", p)
	}
	candidates := []string{
		"scripts/cursor-agent-h2-gateway.mjs",
		"scripts/cursor-agent-stream.mjs",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "share", "sapaloq", "cursor-agent-h2-gateway.mjs"),
			filepath.Join(home, ".local", "share", "sapaloq", "cursor-agent-stream.mjs"),
		)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(exe), "..", "share", "sapaloq", "cursor-agent-h2-gateway.mjs"),
			filepath.Join(filepath.Dir(exe), "..", "share", "sapaloq", "cursor-agent-stream.mjs"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			candidates = append(candidates,
				filepath.Join(dir, "scripts", "cursor-agent-h2-gateway.mjs"),
				filepath.Join(dir, "scripts", "cursor-agent-stream.mjs"),
			)
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				break
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		c = filepath.Clean(c)
		if seen[c] || c == "" {
			continue
		}
		seen[c] = true
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("cursor-agent-h2-gateway.mjs not found (set SAPALOQ_AGENT_H2_GATEWAY)")
}

func AgentNodeStreamAvailable() bool {
	if _, err := agentH2GatewayScriptPath(); err != nil {
		return false
	}
	_, err := exec.LookPath("node")
	return err == nil
}

func classifyAgentAPIError(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "unauthenticated"), strings.Contains(lower, "unauthorized"):
		return "unauthenticated"
	default:
		return "error"
	}
}

// agentNodeStreamScriptPath is kept for tests referencing the legacy name.
func agentNodeStreamScriptPath() (string, error) {
	return agentH2GatewayScriptPath()
}
