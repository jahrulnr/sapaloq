package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

type Orchestrator struct {
	cfgPath  string
	cfg      config.Config
	entry    config.LLMBridge
	bridge   bridge.Bridge
	bus      *bus.Bus
	progress ProgressWriter
	chat     *chatstore.Store
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
	return &Orchestrator{
		cfgPath:  cfgPath,
		cfg:      cfg,
		entry:    entry,
		bridge:   b,
		bus:      eventBus,
		progress: ProgressWriter{Dir: dirs.ProgressDir},
		chat:     chatStore,
	}, nil
}

func (o *Orchestrator) Bus() *bus.Bus { return o.bus }

func (o *Orchestrator) SendChat(ctx context.Context, sessionID, message string) (<-chan bridge.StreamEvent, error) {
	if sessionID == "" {
		var err error
		sessionID, err = o.chat.ActiveSession(ctx, o.entry.Key, o.entry.Model)
		if err != nil {
			return nil, err
		}
	}
	out := make(chan bridge.StreamEvent, 32)
	go func() {
		defer close(out)
		if entry, ok := MatchRegistry(message, o.cfg.Commands); ok {
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
		stream, err := o.bridge.Complete(ctx, bridge.Request{SessionID: sessionID, Messages: messages, Model: o.entry.Model})
		if err != nil {
			debug.Debugf("orchestrator: bridge error session=%s err=%v", sessionID, err)
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return
		}
		var assistant strings.Builder
		for ev := range stream {
			if ev.SessionID == "" {
				ev.SessionID = sessionID
			}
			if ev.Kind == bridge.EventResponseDelta {
				assistant.WriteString(ev.Delta)
			}
			o.emit(ctx, out, ev)
		}
		_ = o.chat.AppendTurn(ctx, sessionID, "assistant", assistant.String(), estimateTextTokens(assistant.String()))
		usage, _ := o.ContextUsage(ctx, sessionID)
		_ = o.chat.SnapshotUsage(ctx, usage)
	}()
	return out, nil
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
