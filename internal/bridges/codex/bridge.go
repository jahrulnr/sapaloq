// Package codex implements the socket-only Codex app-server bridge. A managed
// app-server child is shared across turns; each Complete call opens a JSON-RPC
// WebSocket connection and maps one thread turn into bridge.StreamEvent.
package codex

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/codex/appserver"
	provider "github.com/jahrulnr/sapaloq/internal/bridges/provider"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
)

// Default sandbox posture for programmatic runs. workspace-write lets the agent
// edit within the working root (useful for a coding assistant) but blocks
// arbitrary system writes / network escalation. Conservative by design; never
// danger-full-access by default (CONTRACT §9, DESIGN §12). Overridable via the
// SAPALOQ_CODEX_SANDBOX env var.
const defaultSandbox = "workspace-write"

// Env knobs. We deliberately reuse the existing config.LLMBridge struct rather
// than adding new config fields; the few codex-specific runtime knobs that are
// not part of that struct (binary path, sandbox, cwd, CODEX_HOME) default to a
// safe value and are overridable via environment variables. This keeps the
// config schema unchanged while staying configurable.
const (
	envBinary  = "SAPALOQ_CODEX_BINARY"  // override the resolved `codex` path
	envSandbox = "SAPALOQ_CODEX_SANDBOX" // override the -s sandbox mode
	envCwd     = "SAPALOQ_CODEX_CWD"     // override the -C working root
	envHome    = "CODEX_HOME"            // Codex state root (auth/config/sessions)
	envMode    = "SAPALOQ_CODEX_APP_SERVER_MODE"
	envListen  = "SAPALOQ_CODEX_APP_SERVER_LISTEN"
)

// Bridge is the codex-bridge driver. It holds the resolved binary, the
// SessionID->thread_id store, and the per-turn timeout resolved from config.
type Bridge struct {
	entry   config.LLMBridge
	runtime config.RuntimeConfig
	binary  string
	sandbox string
	cwd     string
	store   *threadStore
	timeout time.Duration
	manager *appserver.Manager
	listen  string
}

// New constructs the bridge: it resolves the codex binary via PATH (never a
// hardcoded release path), opens the thread store under the vault dir, and logs
// `codex --version` so the event-schema assumption is auditable at startup.
func New(entry config.LLMBridge, runtime config.RuntimeConfig) (*Bridge, error) {
	bin := strings.TrimSpace(os.Getenv(envBinary))
	if bin == "" {
		resolved, err := resolveBinary()
		if err != nil {
			return nil, fmt.Errorf("codex-bridge: resolve codex binary: %w (install the Codex CLI or set %s)", err, envBinary)
		}
		bin = resolved
	}

	dirs := config.RuntimeDirs(config.Config{
		Runtime: runtime,
		Events:  config.EventsConfig{Bus: config.BusConfig{SocketPath: ""}},
	})
	store, err := newThreadStore(filepath.Join(dirs.VaultDir, "codex-threads.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("codex-bridge: open thread store: %w", err)
	}

	sandbox := strings.TrimSpace(os.Getenv(envSandbox))
	if sandbox == "" {
		sandbox = defaultSandbox
	}
	cwd := strings.TrimSpace(os.Getenv(envCwd))
	if cwd == "" {
		cwd = dirs.WorkspaceDir
	}

	b := &Bridge{
		entry:   entry,
		runtime: runtime,
		binary:  bin,
		sandbox: sandbox,
		cwd:     cwd,
		store:   store,
		timeout: entry.RequestTimeout(),
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(envMode)))
	if mode == "" {
		mode = appserver.ModeAuto
	}
	if mode != appserver.ModeAuto && mode != appserver.ModeExternal && mode != appserver.ModeManaged {
		return nil, fmt.Errorf("codex-bridge: invalid %s=%q (want auto, external, or managed)", envMode, mode)
	}
	listen, err := b.resolveListen(mode, dirs.RunDir)
	if err != nil {
		return nil, err
	}
	b.listen = listen
	b.manager = &appserver.Manager{Binary: bin, Endpoint: listen, Mode: mode, Env: b.childEnv()}
	debug.Debugf("codex-bridge: binary=%s version=%q mode=%s listen=%s sandbox=%s cwd=%s codex_home=%s",
		bin, b.version(), mode, listen, sandbox, cwd, b.codexHome())
	return b, nil
}

func (b *Bridge) ID() string { return "codex-bridge" }

// Caps reports the bridge capabilities. Thinking/Tools are true (Codex produces
// reasoning and runs tools). LiveAPI reflects real auth: `codex login status`
// exit 0 under the configured CODEX_HOME, OR an API key in env — mirroring how
// cursor.Caps() checks for a token.
func (b *Bridge) Caps() bridge.BridgeCaps {
	return bridge.BridgeCaps{Thinking: true, Tools: true, LiveAPI: b.authOK()}
}

// Complete runs one turn. It mirrors the cursor bridge exactly: a buffered
// channel (cap 32), a goroutine that streams events and always closes the
// channel via defer, and an immediate (out, nil) return.
func (b *Bridge) Complete(ctx context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent, 32)
	go func() {
		defer close(out)
		b.runTurn(ctx, req, out)
	}()
	return out, nil
}

// Close reaps only an app-server process spawned by this bridge. External and
// managed daemon modes never take ownership of the server process.
func (b *Bridge) Close() error {
	if b == nil || b.manager == nil {
		return nil
	}
	return b.manager.Close()
}

// Doctor verifies the binary, lifecycle endpoint, initialize handshake, and
// app-server auth view without running a model turn.
func Doctor(ctx context.Context, entry config.LLMBridge, runtime config.RuntimeConfig) (string, error) {
	b, err := New(entry, runtime)
	if err != nil {
		return "", err
	}
	defer b.Close()
	if err := b.manager.EnsureRunning(ctx); err != nil {
		return "", err
	}
	status, err := appserver.ProbeAuth(ctx, b.listen)
	if err != nil {
		return "", fmt.Errorf("codex-bridge auth probe: %w", err)
	}
	if !b.authOK() && (status.AuthMethod == nil || strings.TrimSpace(*status.AuthMethod) == "") {
		return "", fmt.Errorf("codex-bridge: app-server reachable but Codex is not authenticated; run `codex login`")
	}
	auth := "configured"
	if status.AuthMethod != nil && strings.TrimSpace(*status.AuthMethod) != "" {
		auth = *status.AuthMethod
	}
	return fmt.Sprintf("%s (%s, %s)", auth, b.manager.Mode, b.listen), nil
}

// runTurn drives one app-server turn. A stale resume mapping falls back to a
// fresh thread on the same connection and is overwritten after success.
func (b *Bridge) runTurn(ctx context.Context, req bridge.Request, out chan<- bridge.StreamEvent) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	if err := b.manager.EnsureRunning(ctx); err != nil {
		send(ctx, out, errorEvent(req.SessionID, err.Error()))
		return
	}

	rec, hasResume := b.store.Lookup(req.SessionID)
	hasResume = hasResume && rec.appServerCompatible()
	resumeID := ""
	if hasResume {
		resumeID = rec.ThreadID
	}
	res, err := appserver.RunTurn(ctx, b.listen, appserver.TurnRequest{
		SessionID:    req.SessionID,
		ResumeThread: resumeID,
		FreshPrompt:  composePrompt(req, false),
		ResumePrompt: composePrompt(req, true),
		Model:        firstNonEmpty(req.Model, b.entry.Model),
		Reasoning:    b.safeReasoning(req),
		Cwd:          b.cwd,
		Sandbox:      b.sandbox,
		Images:       req.Images,
		DynamicTools: dynamicTools(req.DeclaredTools),
		ToolExecutor: req.ToolExecutor,
	}, out)
	if err != nil {
		debug.Debugf("codex-bridge: app-server turn error: %v", err)
		send(ctx, out, errorEvent(req.SessionID, "codex-bridge: "+err.Error()))
		return
	}
	if res.ThreadID != "" {
		if serr := b.store.Save(threadRecord{
			SessionID: req.SessionID,
			ThreadID:  res.ThreadID,
			Transport: appServerTransport,
			Cwd:       b.cwd,
			CodexHome: b.codexHome(),
		}); serr != nil {
			debug.Debugf("codex-bridge: persist thread mapping failed: %v", serr)
		}
	}
}

func dynamicTools(names []string) []appserver.DynamicToolNamespace {
	if len(names) == 0 {
		return nil
	}
	tools := make([]appserver.DynamicToolFunction, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		tools = append(tools, appserver.DynamicToolFunction{
			Type: "function", Name: name,
			Description: provider.RegisteredToolDescription(name),
			InputSchema: provider.RegisteredToolSchema(name),
		})
	}
	if len(tools) == 0 {
		return nil
	}
	return []appserver.DynamicToolNamespace{{
		Type: "namespace", Name: "sapaloq",
		Description: "SapaLOQ orchestrator tools", Tools: tools,
	}}
}

// composePrompt builds the stdin prompt. On a resume turn Codex already holds
// the history, so we send only the latest user turn. On a fresh turn we prepend
// a compact transcript (system + prior turns) so the first invocation has the
// conversation context (design §6).
func composePrompt(req bridge.Request, isResume bool) string {
	if isResume {
		return latestUserContent(req.Messages)
	}

	var sys []string
	var convo []bridge.Message
	for _, m := range req.Messages {
		if m.Role == "system" {
			sys = append(sys, m.Content)
			continue
		}
		convo = append(convo, m)
	}

	var b strings.Builder
	if len(sys) > 0 {
		b.WriteString("[system]\n")
		b.WriteString(strings.Join(sys, "\n\n"))
		b.WriteString("\n\n")
	}
	// The latest user turn is sent as the explicit prompt; everything before it
	// is prior conversation context.
	latestIdx := lastUserIndex(convo)
	if latestIdx > 0 {
		b.WriteString("[conversation]\n")
		for i := 0; i < latestIdx; i++ {
			b.WriteString(convo[i].Role)
			b.WriteString(": ")
			b.WriteString(convo[i].Content)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if latestIdx >= 0 {
		b.WriteString("[user]\n")
		b.WriteString(convo[latestIdx].Content)
	} else if len(convo) > 0 {
		// No explicit user turn; fall back to the last message verbatim.
		b.WriteString(convo[len(convo)-1].Content)
	}
	return b.String()
}

// lastUserIndex returns the index of the latest user/tool turn in msgs, or -1.
func lastUserIndex(msgs []bridge.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" || msgs[i].Role == "tool" {
			return i
		}
	}
	return -1
}

// latestUserContent returns the content of the latest user/tool turn (the new
// input on a resume), falling back to the last message.
func latestUserContent(msgs []bridge.Message) string {
	if i := lastUserIndex(msgs); i >= 0 {
		return msgs[i].Content
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content
	}
	return ""
}

// safeReasoning maps the configured ReasoningEffort onto a value safe to pass
// via `-c model_reasoning_effort=<e>`. CONTRACT §7: `minimal` is incompatible
// with the built-in tools (web_search/image_gen) which are on by default, so we
// downgrade `minimal` to `low` when tools may be active rather than triggering a
// 400 turn.failed.
func (b *Bridge) safeReasoning(req bridge.Request) string {
	e := strings.ToLower(strings.TrimSpace(b.entry.ReasoningEffort))
	if e == "" {
		return ""
	}
	if e == "minimal" {
		// Tools are active unless the sandbox is read-only AND no tools declared.
		// Built-in tools are on by default, so treat minimal as unsafe and
		// downgrade. (We never silently pass an invocation we know 400s.)
		debug.Debugf("codex-bridge: reasoning effort 'minimal' is unsafe with built-in tools; downgrading to 'low'")
		return "low"
	}
	return e
}

// childEnv builds the child process environment. It inherits the parent env
// (so CODEX_HOME / ChatGPT auth flow through) and, when CredentialsEnv is set
// and present, injects the value as OPENAI_API_KEY for API-key auth.
func (b *Bridge) childEnv() []string {
	env := os.Environ()
	if name := strings.TrimSpace(b.entry.CredentialsEnv); name != "" {
		if val := strings.TrimSpace(os.Getenv(name)); val != "" {
			// Only set OPENAI_API_KEY if not already present, so an explicit env
			// value is never clobbered.
			if os.Getenv("OPENAI_API_KEY") == "" {
				env = append(env, "OPENAI_API_KEY="+val)
			}
		}
	}
	return env
}

// codexHome returns the effective CODEX_HOME (explicit env or the default
// ~/.codex), used for logging and the thread record.
func (b *Bridge) codexHome() string {
	if h := strings.TrimSpace(os.Getenv(envHome)); h != "" {
		return h
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".codex")
	}
	return "~/.codex"
}

func (b *Bridge) resolveListen(mode, runDir string) (string, error) {
	raw := strings.TrimSpace(os.Getenv(envListen))
	if raw == "" {
		if mode == appserver.ModeManaged {
			raw = "unix://" + filepath.Join(b.codexHome(), "app-server-control", "app-server-control.sock")
		} else {
			raw = "unix://" + filepath.Join(runDir, "codex-app-server.sock")
		}
	}
	if strings.HasPrefix(raw, "unix://") {
		path := strings.TrimPrefix(raw, "unix://")
		if strings.HasPrefix(path, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("codex-bridge: expand app-server listen path: %w", err)
			}
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
		if !filepath.IsAbs(path) {
			abs, err := filepath.Abs(path)
			if err != nil {
				return "", fmt.Errorf("codex-bridge: resolve app-server listen path: %w", err)
			}
			path = abs
		}
		return "unix://" + filepath.Clean(path), nil
	}
	if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
		return raw, nil
	}
	return "", fmt.Errorf("codex-bridge: unsupported %s=%q (want unix://, ws://, or wss://)", envListen, raw)
}

// authOK reports whether Codex is authenticated, mirroring how cursor.Caps()
// checks for a token. It prefers a cheap `codex login status` (exit 0 ==
// authenticated, no model call) and falls back to detecting an API key in env.
func (b *Bridge) authOK() bool {
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" ||
		strings.TrimSpace(os.Getenv("CODEX_API_KEY")) != "" {
		return true
	}
	if name := strings.TrimSpace(b.entry.CredentialsEnv); name != "" && strings.TrimSpace(os.Getenv(name)) != "" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.binary, "login", "status")
	cmd.Env = b.childEnv()
	return cmd.Run() == nil
}

// version best-effort returns `codex --version` output for the startup log.
func (b *Bridge) version() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.binary, "--version")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "unknown"
	}
	return strings.TrimSpace(buf.String())
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
