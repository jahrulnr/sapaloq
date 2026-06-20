package orchestrator

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

type Orchestrator struct {
	cfgPath    string
	cfg        config.Config
	entry      config.LLMBridge
	bridge     bridge.Bridge
	bus        *bus.Bus
	progress   ProgressWriter
	chat       *chatstore.Store
	memoryDir  string
	mu         sync.RWMutex
	cfgModTime time.Time
}

func New(cfg config.Config, cfgPath string, b bridge.Bridge, eventBus *bus.Bus) (*Orchestrator, error) {
	entry, err := cfg.LLMBridge.ActiveProvider()
	if err != nil {
		return nil, err
	}
	dirs := config.RuntimeDirs(cfg)
	chatStore, err := chatstore.Open(dirs.MemoryDir)
	if err != nil {
		return nil, fmt.Errorf("chat store: %w", err)
	}
	var modTime time.Time
	if cfgPath != "" {
		if info, statErr := os.Stat(cfgPath); statErr == nil {
			modTime = info.ModTime()
		}
	}
	return &Orchestrator{
		cfgPath:    cfgPath,
		cfg:        cfg,
		entry:      entry,
		bridge:     b,
		bus:        eventBus,
		progress:   ProgressWriter{Dir: dirs.ProgressDir},
		chat:       chatStore,
		memoryDir:  dirs.MemoryDir,
		cfgModTime: modTime,
	}, nil
}

func (o *Orchestrator) Bus() *bus.Bus { return o.bus }

func (o *Orchestrator) SendChat(ctx context.Context, sessionID, message string) (<-chan bridge.StreamEvent, error) {
	o.reloadConfigIfChanged(ctx)
	snap := o.snapshot()
	if sessionID == "" {
		var err error
		sessionID, err = o.chat.ActiveSession(ctx, snap.entry.Key, snap.entry.Model)
		if err != nil {
			return nil, err
		}
	}
	out := make(chan bridge.StreamEvent, 32)
	go func() {
		defer close(out)
		if entry, ok := MatchRegistry(message, snap.cfg.Commands); ok {
			debug.Debugf("orchestrator: slash route id=%s session=%s", entry.ID, sessionID)
			o.handleSlash(ctx, out, sessionID, entry.ID, message)
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			return
		}
		_ = o.chat.AppendTurn(ctx, sessionID, "user", message, estimateTextTokens(message))
		messages, err := o.contextMessages(ctx, sessionID, message)
		if err != nil {
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return
		}
		stream, err := snap.br.Complete(ctx, bridge.Request{SessionID: sessionID, Messages: messages, Model: snap.entry.Model, DeclaredTools: askTools})
		if err != nil {
			debug.Debugf("orchestrator: bridge error session=%s err=%v", sessionID, err)
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return
		}
		var assistant strings.Builder
		var toolResponses []string
		for ev := range stream {
			if ev.SessionID == "" {
				ev.SessionID = sessionID
			}
			if ev.Kind == bridge.EventResponseDelta {
				assistant.WriteString(ev.Delta)
			}
			if ev.Kind == bridge.EventToolCall && ev.ToolCall != nil {
				if response, handled := o.handleAskTool(ctx, snap, sessionID, message, *ev.ToolCall); handled {
					toolResponses = append(toolResponses, response)
				}
			}
			o.emit(ctx, out, ev)
		}
		for _, response := range toolResponses {
			assistant.WriteString(response)
			o.emit(ctx, out, responseEvent(sessionID, response))
		}
		_ = o.chat.AppendTurn(ctx, sessionID, "assistant", assistant.String(), estimateTextTokens(assistant.String()))
		usage, _ := o.ContextUsage(ctx, sessionID)
		_ = o.chat.SnapshotUsage(ctx, usage)
	}()
	return out, nil
}

// RetryChat regenerates the response for an existing user turn. It preserves
// that user turn, removes only its descendants, and does not append a duplicate
// user message.
func (o *Orchestrator) RetryChat(ctx context.Context, sessionID string, turnID int64) (<-chan bridge.StreamEvent, error) {
	if turnID <= 0 {
		return nil, fmt.Errorf("turn id is required")
	}
	o.reloadConfigIfChanged(ctx)
	snap := o.snapshot()
	if sessionID == "" {
		var err error
		sessionID, err = o.chat.ActiveSession(ctx, snap.entry.Key, snap.entry.Model)
		if err != nil {
			return nil, err
		}
	}
	turn, err := o.chat.Turn(ctx, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	if turn.Role != "user" {
		return nil, fmt.Errorf("turn %d is not a user message", turnID)
	}
	if err := o.chat.DeleteAfterTurn(ctx, sessionID, turnID); err != nil {
		return nil, err
	}
	out := make(chan bridge.StreamEvent, 32)
	go o.completeExistingTurn(ctx, snap, out, sessionID, turn.Content)
	return out, nil
}

func (o *Orchestrator) completeExistingTurn(ctx context.Context, snap providerSnapshot, out chan bridge.StreamEvent, sessionID, message string) {
	defer close(out)
	messages, err := o.contextMessages(ctx, sessionID, message)
	if err != nil {
		o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
		return
	}
	stream, err := snap.br.Complete(ctx, bridge.Request{SessionID: sessionID, Messages: messages, Model: snap.entry.Model, DeclaredTools: askTools})
	if err != nil {
		o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
		return
	}
	var assistant strings.Builder
	var toolResponses []string
	for ev := range stream {
		if ev.SessionID == "" {
			ev.SessionID = sessionID
		}
		if ev.Kind == bridge.EventResponseDelta {
			assistant.WriteString(ev.Delta)
		}
		if ev.Kind == bridge.EventToolCall && ev.ToolCall != nil {
			if response, handled := o.handleAskTool(ctx, snap, sessionID, message, *ev.ToolCall); handled {
				toolResponses = append(toolResponses, response)
			}
		}
		o.emit(ctx, out, ev)
	}
	for _, response := range toolResponses {
		assistant.WriteString(response)
		o.emit(ctx, out, responseEvent(sessionID, response))
	}
	_ = o.chat.AppendTurn(ctx, sessionID, "assistant", assistant.String(), estimateTextTokens(assistant.String()))
	usage, _ := o.ContextUsage(ctx, sessionID)
	_ = o.chat.SnapshotUsage(ctx, usage)
}

func (o *Orchestrator) emit(ctx context.Context, out chan<- bridge.StreamEvent, ev bridge.StreamEvent) bool {
	_ = o.progress.Append(ev.SessionID, ev)
	if o.bus != nil {
		o.bus.Publish(topicFor(ev.Kind), ev)
	}
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}

func settingsEvent(sessionID, id string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = fmt.Sprintf("/%s handler is registered; config patch sub-agent is TODO for MVP.", id)
	return ev
}

func topicFor(kind bridge.EventKind) string {
	switch kind {
	case bridge.EventThinkingDelta:
		return "sapaloq.v1.chat.thinking"
	case bridge.EventResponseDelta:
		return "sapaloq.v1.chat.response"
	case bridge.EventToolCall:
		return "sapaloq.v1.chat.tool_call"
	default:
		return "sapaloq.v1.chat." + string(kind)
	}
}
