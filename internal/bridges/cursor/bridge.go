package cursor

import (
	"context"
	"encoding/json"
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
	"github.com/jahrulnr/sapaloq/internal/parse/tools/kimi"
	"github.com/jahrulnr/sapaloq/internal/vault"
)

type Bridge struct {
	entry   config.LLMBridge
	runtime config.RuntimeConfig
	schema  Schema
	vault   *vault.Writer
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
	return &Bridge{entry: entry, runtime: runtime, schema: schema, vault: v}, nil
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

	// Route to Agent API path when vision input is detected OR SAPALOQ_AGENT_PATH is forced.
	if wantsAgentPath(req) {
		b.streamLiveAgent(ctx, req, creds, out)
		return
	}
	messages := make([]wire.ChatMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		messages = append(messages, wire.ChatMessage{Role: msg.Role, Content: msg.Content})
	}
	var responseBuf strings.Builder
	var frameCount int
	// Driver selection. Default uses the raw HTTP/2 client (mirrors cursor-proto-lab
	// byte for byte). Set SAPALOQ_WIRE_DRIVER=http2 to fall back to Go's net/http2.
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("SAPALOQ_WIRE_DRIVER")))
	streamFn := wire.StreamChatRaw
	if driver == "http2" {
		streamFn = wire.StreamChat
	}
	err = streamFn(ctx, wire.StreamOptions{
		Endpoint:    b.entry.Endpoint,
		Token:       creds.AccessToken,
		MachineID:   creds.MachineID,
		Model:       defaultIfEmpty(req.Model, b.entry.Model),
		Messages:    messages,
		GhostMode:   creds.GhostMode,
		InsecureTLS: os.Getenv("SAPALOQ_WIRE_INSECURE_TLS") == "1",
	}, func(part wire.ExtractedPart) {
		frameCount++
		debug.Verbosef("cursor-bridge: frame=%d thinking=%d text=%d tool=%v decode_err=%q",
			frameCount, len(part.Thinking), len(part.Text), part.ToolCall != nil, part.DecodeErr)
		if part.Thinking != "" {
			ev := bridge.NewEvent(bridge.EventThinkingDelta)
			ev.SessionID = req.SessionID
			ev.Delta = part.Thinking
			send(ctx, out, ev)
		}
		if part.Text != "" {
			responseBuf.WriteString(part.Text)
			ev := bridge.NewEvent(bridge.EventResponseDelta)
			ev.SessionID = req.SessionID
			ev.Delta = part.Text
			send(ctx, out, ev)
			for _, call := range kimi.ParseInlineWithTokens(part.Text, b.schema.KimiTokens()) {
				b.emitToolCall(ctx, out, req.SessionID, call)
			}
		}
		if part.ToolCall != nil {
			raw, _ := json.Marshal(map[string]any{
				"id":        part.ToolCall.ID,
				"name":      part.ToolCall.Name,
				"arguments": json.RawMessage(part.ToolCall.Arguments),
			})
			if call, ok := toolcursor.ParseClientSideToolV2Call(raw); ok {
				b.emitToolCall(ctx, out, req.SessionID, call)
			}
		}
	})
	if err != nil {
		debug.Debugf("cursor-bridge: stream error: %v", err)
		errEv := bridge.NewEvent(bridge.EventError)
		errEv.SessionID = req.SessionID
		errEv.Error = err.Error()
		send(ctx, out, errEv)
		return
	}
	debug.Debugf("cursor-bridge: stream done frames=%d response_bytes=%d", frameCount, responseBuf.Len())
	done := bridge.NewEvent(bridge.EventDone)
	done.SessionID = req.SessionID
	send(ctx, out, done)
}

func (b *Bridge) streamMock(ctx context.Context, req bridge.Request, out chan<- bridge.StreamEvent) {
	message := lastUserMessage(req.Messages)
	thinking := bridge.NewEvent(bridge.EventThinkingDelta)
	thinking.SessionID = req.SessionID
	thinking.Delta = "No SAPALOQ_CURSOR_TOKEN — using offline mock stream."
	send(ctx, out, thinking)

	if strings.Contains(message, "glob") || strings.Contains(message, "tool") {
		if call, ok := toolcursor.ParseClientSideToolV2Call([]byte(`{"id":"mock-1","name":"glob","arguments":{"pattern":"*.go"}}`)); ok {
			b.emitToolCall(ctx, out, req.SessionID, call)
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

func (b *Bridge) emitToolCall(ctx context.Context, out chan<- bridge.StreamEvent, sessionID string, call parse.ToolCall) {
	rawName := call.Name
	coerced := CoerceToolCall(b.schema, call)
	if reason := VaultReason(b.schema, b.entry.DeclaredTools, rawName, coerced); reason != "" {
		debug.Debugf("cursor-bridge: vault %s raw=%s resolved=%s", reason, rawName, coerced.Name)
		_ = b.vault.Append(vault.Entry{
			SessionID:    sessionID,
			Provider:     b.ID(),
			RawName:      rawName,
			ResolvedName: coerced.Name,
			Arguments:    coerced.Arguments,
			Source:       coerced.Source,
			Reason:       reason,
		})
	}
	ev := bridge.NewEvent(bridge.EventToolCall)
	ev.SessionID = sessionID
	ev.ToolCall = &coerced
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
		if messages[i].Role == "user" {
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
