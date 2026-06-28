package cursor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/wire"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/parse"
	thinkingcursor "github.com/jahrulnr/sapaloq/internal/parse/thinking/cursor"
	toolcursor "github.com/jahrulnr/sapaloq/internal/parse/tools/cursor"
	"github.com/jahrulnr/sapaloq/internal/vault"
)

type Bridge struct {
	entry   config.LLMBridge
	runtime config.RuntimeConfig
	schema  Schema
	vault   *vault.Writer
	// timeout bounds a single inference request; resolved from the provider
	// entry (config) so a long sub-agent step isn't truncated at the old
	// hardcoded 120s wire default.
	timeout time.Duration
}

// New returns a fresh Bridge. The entry supplies the bridge configuration
// (endpoint, model, credentials env, declared tools). The runtime config
// supplies the vault directory.
func New(entry config.LLMBridge, runtime config.RuntimeConfig) (*Bridge, error) {
	schema, err := LoadSchema()
	if err != nil {
		return nil, err
	}
	dirs := config.RuntimeDirs(config.Config{
		Runtime: runtime,
		Events:  config.EventsConfig{Bus: config.BusConfig{SocketPath: ""}},
	})
	v, err := vault.New(filepath.Join(dirs.VaultDir, "tool-calls.jsonl"))
	if err != nil {
		return nil, err
	}
	return &Bridge{entry: entry, runtime: runtime, schema: schema, vault: v, timeout: entry.RequestTimeout()}, nil
}

func (b *Bridge) ID() string { return "cursor-bridge" }

func (b *Bridge) Caps() bridge.BridgeCaps {
	creds, err := b.loadCreds()
	return bridge.BridgeCaps{Thinking: true, Tools: true, LiveAPI: err == nil && creds.AccessToken != ""}
}

func (b *Bridge) Complete(ctx context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent, 32)
	go func() {
		defer close(out)
		live := b.hasToken()
		debug.Debugf("cursor-bridge: complete session=%s live=%v messages=%d", req.SessionID, live, len(req.Messages))
		if live {
			b.streamLive(ctx, req, out)
			return
		}
		debug.Debugf("cursor-bridge: falling back to mock stream")
		b.streamMock(ctx, req, out)
	}()
	return out, nil
}

func (b *Bridge) loadCreds() (credentials.Credentials, error) {
	return credentials.Load(credentials.Options{TokenEnv: b.entry.CredentialsEnv})
}

func (b *Bridge) hasToken() bool {
	creds, err := b.loadCreds()
	return err == nil && creds.AccessToken != ""
}

func (b *Bridge) streamLive(ctx context.Context, req bridge.Request, out chan<- bridge.StreamEvent) {
	creds, err := b.loadCreds()
	if err != nil || creds.AccessToken == "" {
		debug.Debugf("cursor-bridge: live stream aborted cred_err=%v", err)
		errEv := bridge.NewEvent(bridge.EventError)
		errEv.SessionID = req.SessionID
		if err != nil {
			errEv.Error = err.Error()
		} else {
			errEv.Error = "cursor credentials missing"
		}
		send(ctx, out, errEv)
		return
	}
	debug.Debugf("cursor-bridge: creds source=%s token=%s machine=%s ghost=%v",
		creds.Source, debug.RedactSecret(creds.AccessToken), debug.RedactSecret(creds.MachineID), creds.GhostMode)

	// Route to Agent API for vision; chat stream uses Node wire when available.
	if wantsAgentPath(req) {
		b.streamLiveAgent(ctx, req, creds, out)
		return
	}
	messages := normalizeCursorWireMessages(req.Messages)
	model := defaultIfEmpty(req.Model, b.entry.Model)
	upstreamModel := ResolveCursorUpstreamModel(model)
	declared := declaredToolsForRequest(req.DeclaredTools, b.entry.DeclaredTools)
	guard := b.schema.BuildGuardContext(model, declared, req.Messages)
	userPrompt := guard.UserPrompt
	wireTools := buildWireMCPTools(declared)
	kimiTokens := b.schema.KimiTokens()
	promoteThinking := ShouldPromoteThinkingToContent(model)
	acc := newLiveTurnBuffer(kimiTokens, promoteThinking)
	var frameCount int
	// Driver selection. Default uses Node (cursor-proto-lab) when available because
	// Go raw/http2 drivers are rejected by api2 with valid vscdb tokens.
	// Override: SAPALOQ_WIRE_DRIVER=raw|http2|node
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("SAPALOQ_WIRE_DRIVER")))
	streamFn := wire.StreamChatRaw
	switch driver {
	case "http2":
		streamFn = wire.StreamChat
	case "node":
		streamFn = wire.StreamChatNode
	case "raw":
		streamFn = wire.StreamChatRaw
	default:
		if wire.NodeStreamAvailable() {
			streamFn = wire.StreamChatNode
		}
	}
	err = streamFn(ctx, wire.StreamOptions{
		Endpoint:        b.entry.Endpoint,
		Token:           creds.AccessToken,
		MachineID:       creds.MachineID,
		Model:           upstreamModel,
		Messages:        messages,
		Tools:           wireTools,
		Instruction:     guard.Instruction,
		ForceAgentMode:  guard.ForceAgentMode,
		ReasoningEffort: b.entry.ReasoningEffort,
		GhostMode:       creds.GhostMode,
		InsecureTLS:     os.Getenv("SAPALOQ_WIRE_INSECURE_TLS") == "1",
		Timeout:         b.timeout,
	}, func(part wire.ExtractedPart) {
		frameCount++
		debug.Verbosef("cursor-bridge: frame=%d thinking=%d text=%d tool=%v decode_err=%q",
			frameCount, len(part.Thinking), len(part.Text), part.ToolCall != nil, part.DecodeErr)
		acc.ingest(part)
	})
	if err != nil {
		debug.Debugf("cursor-bridge: stream error: %v", err)
		errEv := bridge.NewEvent(bridge.EventError)
		errEv.SessionID = req.SessionID
		errEv.Error = b.explainStreamError(err)
		send(ctx, out, errEv)
		return
	}
	responseBytes, noiseDropped := b.finalizeBufferedTurn(ctx, out, req.SessionID, declared, guard, userPrompt, acc)
	debug.Debugf("cursor-bridge: stream done frames=%d response_bytes=%d noise_dropped=%v", frameCount, responseBytes, noiseDropped)
	if frameCount == 0 && responseBytes == 0 && !noiseDropped {
		debug.Debugf("cursor-bridge: empty turn (no frames) - emitting done for orchestrator nudge")
	}
	done := bridge.NewEvent(bridge.EventDone)
	done.SessionID = req.SessionID
	send(ctx, out, done)
}

// explainStreamError turns an opaque transport failure into an actionable
// message. A bare "context deadline exceeded" tells the user nothing; this maps
// it to the real cause (the per-request timeout) and the knob to raise it.
func (b *Bridge) explainStreamError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "deadline exceeded") {
		secs := int(b.timeout / time.Second)
		if secs <= 0 {
			secs = config.DefaultRequestTimeoutSec
		}
		return fmt.Sprintf("inference request timed out after %ds (set llmBridge.providers[].requestTimeoutSec higher for long sub-agent steps): %s", secs, msg)
	}
	return msg
}

func (b *Bridge) streamMock(ctx context.Context, req bridge.Request, out chan<- bridge.StreamEvent) {
	message := lastUserMessage(req.Messages)
	thinking := bridge.NewEvent(bridge.EventThinkingDelta)
	thinking.SessionID = req.SessionID
	thinking.Delta = "No SAPALOQ_CURSOR_TOKEN - using offline mock stream."
	send(ctx, out, thinking)

	// Autopilot continuations ask for sapaloq_stop; honor them so the
	// orchestrator does not burn the inference-turn budget when mock mode is
	// active (e.g. tests or missing credentials).
	if strings.Contains(message, "<sapaloq:autopilot>") {
		if call, ok := toolcursor.ParseClientSideToolV2Call([]byte(`{"id":"mock-stop","name":"sapaloq_stop","arguments":{"reason":"offline mock stop"}}`)); ok {
			b.emitToolCall(ctx, out, req.SessionID, call)
		}
		done := bridge.NewEvent(bridge.EventDone)
		done.SessionID = req.SessionID
		send(ctx, out, done)
		return
	}

	if strings.Contains(message, "undeclared_probe") {
		call := parse.ToolCall{Name: "glob_file_search", Source: "kimi_inline"}
		b.tryEmitToolCall(ctx, out, req.SessionID, declaredToolsForRequest(req.DeclaredTools, b.entry.DeclaredTools), call)
	}

	if strings.Contains(message, "glob") || strings.Contains(message, "tool") {
		if call, ok := toolcursor.ParseClientSideToolV2Call([]byte(`{"id":"mock-1","name":"glob","arguments":{"glob_pattern":"*.go"}}`)); ok {
			coerced := CoerceToolCall(b.schema, call)
			declared := declaredToolsForRequest(req.DeclaredTools, b.entry.DeclaredTools)
			if coerced.Name == "glob_file_search" && foldToolName(call.Name) == "glob" {
				b.emitToolCall(ctx, out, req.SessionID, coerced)
			} else {
				b.tryEmitToolCall(ctx, out, req.SessionID, declared, call)
			}
		}
	}

	response := bridge.NewEvent(bridge.EventResponseDelta)
	response.SessionID = req.SessionID
	response.Delta = fmt.Sprintf("SapaLOQ received: %s", message)
	send(ctx, out, response)

	done := bridge.NewEvent(bridge.EventDone)
	done.SessionID = req.SessionID
	send(ctx, out, done)
}

func (b *Bridge) tryEmitToolCall(ctx context.Context, out chan<- bridge.StreamEvent, sessionID string, declared []string, call parse.ToolCall) {
	coerced := CoerceToolCall(b.schema, call)
	if reason := VaultReason(b.schema, declared, call.Name, coerced); reason != "" {
		debug.Debugf("cursor-bridge: drop tool %s (%s) raw=%s resolved=%s", reason, coerced.Source, call.Name, coerced.Name)
		_ = b.vault.Append(vault.Entry{
			SessionID:    sessionID,
			Provider:     b.ID(),
			RawName:      call.Name,
			ResolvedName: coerced.Name,
			Arguments:    coerced.Arguments,
			Source:       coerced.Source,
			Reason:       reason,
		})
		return
	}
	b.emitToolCall(ctx, out, sessionID, coerced)
}

func (b *Bridge) emitToolCall(ctx context.Context, out chan<- bridge.StreamEvent, sessionID string, call parse.ToolCall) {
	ev := bridge.NewEvent(bridge.EventToolCall)
	ev.SessionID = sessionID
	ev.ToolCall = &call
	send(ctx, out, ev)
}

func send(ctx context.Context, out chan<- bridge.StreamEvent, ev bridge.StreamEvent) bool {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}

func lastUserMessage(messages []bridge.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		// A "tool" turn is fresh input to the model (a tool observation), so it
		// counts as the latest user-side message alongside a real "user" turn.
		if messages[i].Role == "user" || messages[i].Role == "tool" {
			return messages[i].Content
		}
	}
	if len(messages) > 0 {
		return messages[len(messages)-1].Content
	}
	return ""
}

func defaultIfEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// PostTagVisible splits thinking/response for no-9router-collapse regression.
func PostTagVisible(text string) (thinking, response string) {
	parsed := thinkingcursor.ParseCursorThinking(text)
	return parsed.Thinking, parsed.Response
}
