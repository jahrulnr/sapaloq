package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
)

type Orchestrator struct {
	cfgPath  string
	cfg      config.Config
	bridge   bridge.Bridge
	bus      *bus.Bus
	progress ProgressWriter
}

func New(cfg config.Config, cfgPath string, b bridge.Bridge, eventBus *bus.Bus) *Orchestrator {
	return &Orchestrator{
		cfgPath:  cfgPath,
		cfg:      cfg,
		bridge:   b,
		bus:      eventBus,
		progress: ProgressWriter{Dir: config.RuntimeDirs(cfg).ProgressDir},
	}
}

func (o *Orchestrator) Bus() *bus.Bus { return o.bus }

func (o *Orchestrator) SendChat(ctx context.Context, sessionID, message string) (<-chan bridge.StreamEvent, error) {
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	out := make(chan bridge.StreamEvent, 32)
	go func() {
		defer close(out)
		if entry, ok := MatchRegistry(message, o.cfg.Commands); ok {
			debug.Debugf("orchestrator: slash route id=%s session=%s", entry.ID, sessionID)
			if entry.ID == "settings" {
				o.handleSettings(ctx, out, sessionID, message)
			} else {
				o.emit(ctx, out, settingsEvent(sessionID, entry.ID))
			}
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			return
		}
		stream, err := o.bridge.Complete(ctx, bridge.Request{SessionID: sessionID, Messages: []bridge.Message{{Role: "user", Content: message}}, Model: o.cfg.LLMBridge.Model})
		if err != nil {
			debug.Debugf("orchestrator: bridge error session=%s err=%v", sessionID, err)
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return
		}
		for ev := range stream {
			if ev.SessionID == "" {
				ev.SessionID = sessionID
			}
			o.emit(ctx, out, ev)
		}
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
