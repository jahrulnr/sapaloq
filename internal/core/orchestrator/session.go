package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

const (
	defaultContextWindow = 1000000
	autoCompactPercent   = 80
)

func estimateTextTokens(text string) int {
	return (len(text) + 3) / 4
}

func (o *Orchestrator) contextWindow() int {
	if o.entry.ContextWindow > 0 {
		return o.entry.ContextWindow
	}
	return defaultContextWindow
}

func (o *Orchestrator) contextMessages(ctx context.Context, sessionID, latestUserMessage string) ([]bridge.Message, error) {
	usage, err := o.ContextUsage(ctx, sessionID)
	if err == nil && usage.ContextWindow > 0 && usage.Percent >= autoCompactPercent {
		_, _ = o.compactActiveSession(ctx, sessionID, "auto")
	}
	turns, err := o.chat.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return nil, err
	}
	messages := make([]bridge.Message, 0, len(turns)+1)
	messages = append(messages, bridge.Message{Role: "system", Content: "You are SapaLOQ. Use the active-session context below. Compacted summaries are authoritative; do not ask the user to repeat preserved context."})
	for _, turn := range turns {
		role := turn.Role
		if role == "tool" || role == "error" {
			role = "assistant"
		}
		messages = append(messages, bridge.Message{Role: role, Content: turn.Content})
	}
	if len(turns) == 0 || turns[len(turns)-1].Content != latestUserMessage {
		messages = append(messages, bridge.Message{Role: "user", Content: latestUserMessage})
	}
	return messages, nil
}

func (o *Orchestrator) handleSlash(ctx context.Context, out chan<- bridge.StreamEvent, sessionID, id, message string) {
	switch id {
	case "settings":
		o.handleSettings(ctx, out, sessionID, message)
	case "compaction":
		count, err := o.compactActiveSession(ctx, sessionID, "manual")
		if err != nil {
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return
		}
		o.emit(ctx, out, responseEvent(sessionID, fmt.Sprintf("Compaction complete. %d older turns summarized.", count)))
	case "reset":
		newID, err := o.chat.Reset(ctx, o.entry.Key, o.entry.Model)
		if err != nil {
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return
		}
		o.emit(ctx, out, responseEvent(newID, "Session reset. Starting a fresh active chat."))
	default:
		o.emit(ctx, out, settingsEvent(sessionID, id))
	}
}

func (o *Orchestrator) compactActiveSession(ctx context.Context, sessionID, reason string) (int, error) {
	turns, err := o.chat.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return 0, err
	}
	if len(turns) <= 6 {
		return 0, nil
	}
	var b strings.Builder
	b.WriteString("[Compacted active-session summary")
	if reason != "" {
		b.WriteString("; reason=")
		b.WriteString(reason)
	}
	b.WriteString("]\n")
	for _, t := range turns[:len(turns)-4] {
		line := strings.TrimSpace(t.Content)
		if line == "" {
			continue
		}
		if len(line) > 240 {
			line = line[:240] + "…"
		}
		b.WriteString("- ")
		b.WriteString(t.Role)
		b.WriteString(": ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return o.chat.Compact(ctx, sessionID, 4, b.String(), estimateTextTokens)
}

func (o *Orchestrator) ActiveSession(ctx context.Context) (string, error) {
	return o.chat.ActiveSession(ctx, o.entry.Key, o.entry.Model)
}

func (o *Orchestrator) ActiveTurns(ctx context.Context, sessionID string) ([]chatstore.Turn, error) {
	if sessionID == "" {
		var err error
		sessionID, err = o.ActiveSession(ctx)
		if err != nil {
			return nil, err
		}
	}
	return o.chat.ActiveTurns(ctx, sessionID, true)
}

func (o *Orchestrator) ContextUsage(ctx context.Context, sessionID string) (chatstore.Usage, error) {
	if sessionID == "" {
		var err error
		sessionID, err = o.ActiveSession(ctx)
		if err != nil {
			return chatstore.Usage{}, err
		}
	}
	return o.chat.Usage(ctx, sessionID, o.entry.Key, o.entry.Model, o.contextWindow())
}

func responseEvent(sessionID, text string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = text
	return ev
}
