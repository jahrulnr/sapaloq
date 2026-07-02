package gemini

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
)

// Bridge implements bridge.Bridge for Google Generative Language generateContent.
type Bridge struct {
	entry config.LLMBridge
}

func New(entry config.LLMBridge) (*Bridge, error) {
	if strings.TrimSpace(entry.Endpoint) == "" {
		return nil, fmt.Errorf("gemini-bridge: endpoint is required")
	}
	if strings.TrimSpace(entry.Model) == "" {
		return nil, fmt.Errorf("gemini-bridge: model is required")
	}
	return &Bridge{entry: entry}, nil
}

func (b *Bridge) ID() string { return driverID }

func (b *Bridge) Caps() bridge.BridgeCaps {
	return bridge.BridgeCaps{
		Thinking: true,
		Tools:    true,
		LiveAPI:  resolveAPIKey(b.entry) != "",
	}
}

func (b *Bridge) Complete(ctx context.Context, req bridge.Request) (<-chan bridge.StreamEvent, error) {
	out := make(chan bridge.StreamEvent, 32)
	if resolveAPIKey(b.entry) == "" {
		errEv := bridge.NewEvent(bridge.EventError)
		errEv.SessionID = req.SessionID
		cred := strings.TrimSpace(b.entry.CredentialsEnv)
		if cred == "" {
			cred = "GEMINI_API_KEY"
		}
		errEv.Error = fmt.Sprintf("gemini-bridge: token env %s (or GOOGLE_API_KEY) is empty", cred)
		go func() {
			defer close(out)
			out <- errEv
		}()
		return out, nil
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = b.entry.Model
	}
	entry := b.entry
	entry.Model = model

	declared := req.DeclaredTools
	if len(declared) == 0 {
		declared = entry.DeclaredTools
	}

	go func() {
		defer close(out)
		b.runComplete(ctx, out, req.SessionID, entry, req.Messages, declared, req.Images)
	}()
	return out, nil
}

func (b *Bridge) runComplete(ctx context.Context, out chan<- bridge.StreamEvent, sessionID string, entry config.LLMBridge, messages []bridge.Message, declared []string, images []bridge.Image) {
	emit := func(ev bridge.StreamEvent) bool {
		select {
		case <-ctx.Done():
			return false
		case out <- ev:
			return true
		}
	}

	debug.Debugf("gemini-bridge: complete session=%s model=%s stream=%v tools=%d",
		sessionID, entry.Model, entry.StreamEnabled(), len(declared))

	turn, err := completeWithFallbacks(ctx, entry, messages, declared, entry.StreamEnabled(), images)
	if err != nil {
		ev := bridge.NewEvent(bridge.EventError)
		ev.SessionID = sessionID
		ev.Error = err.Error()
		emit(ev)
		return
	}

	if turn.thinking != "" {
		ev := bridge.NewEvent(bridge.EventThinkingDelta)
		ev.SessionID = sessionID
		ev.Delta = turn.thinking
		if !emit(ev) {
			return
		}
	}
	if turn.content != "" {
		ev := bridge.NewEvent(bridge.EventResponseDelta)
		ev.SessionID = sessionID
		ev.Delta = turn.content
		if !emit(ev) {
			return
		}
	}

	wireMeta := encodeWireMeta(turn.modelParts)
	for _, tc := range turn.toolCalls {
		call := toParseToolCall(tc)
		ev := bridge.NewEvent(bridge.EventToolCall)
		ev.SessionID = sessionID
		ev.ToolCall = &call
		if len(wireMeta) > 0 {
			ev.WireMeta = wireMeta
		}
		if !emit(ev) {
			return
		}
	}

	ev := bridge.NewEvent(bridge.EventDone)
	ev.SessionID = sessionID
	emit(ev)
}
