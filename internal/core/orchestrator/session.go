package orchestrator

// session.go owns the live-session lifecycle for the Ask / chat role:
// ActiveSession, ActiveTurns, DeleteTurn, SubmitFeedback, ContextUsage,
// handleSlash, and compactActiveSession. The system-prompt resolution and
// per-turn system-block builders that used to live here moved to prompt.go,
// which is now the single place to look when you want to know exactly what
// the model sees on every turn.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/prompts"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

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
		snap := o.snapshot()
		newID, err := o.chat.Reset(ctx, snap.entry.Key, snap.entry.Model)
		if err != nil {
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return
		}
		o.emit(ctx, out, responseEvent(newID, "Session reset. Starting a fresh active chat."))
	case "model":
		o.handleModel(ctx, out, sessionID, message)
	case "thinking":
		o.handleThinking(ctx, out, sessionID, message)
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
		if t.Role == "thinking" {
			continue // reasoning is UI-only; don't bloat the compaction summary
		}
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
	snap := o.snapshot()
	return o.chat.ActiveSession(ctx, snap.entry.Key, snap.entry.Model)
}

// ListSessions returns recent chat sessions for the widget's history switcher,
// most-recently-updated first (active session sorted to the top).
func (o *Orchestrator) ListSessions(ctx context.Context, limit int) ([]chatstore.SessionSummary, error) {
	return o.chat.ListSessions(ctx, limit)
}

// SwitchSession makes an existing session the active one and returns its id so
// the caller can immediately restore that session's history.
func (o *Orchestrator) SwitchSession(ctx context.Context, sessionID string) (string, error) {
	if err := o.chat.Activate(ctx, sessionID); err != nil {
		return "", err
	}
	return sessionID, nil
}

// NewSession starts a fresh active chat session (same path as the /reset slash
// command) and returns the new session id.
func (o *Orchestrator) NewSession(ctx context.Context) (string, error) {
	snap := o.snapshot()
	return o.chat.Reset(ctx, snap.entry.Key, snap.entry.Model)
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

func (o *Orchestrator) DeleteTurn(ctx context.Context, sessionID string, turnID int64) error {
	if sessionID == "" {
		var err error
		sessionID, err = o.ActiveSession(ctx)
		if err != nil {
			return err
		}
	}
	return o.chat.DeleteFromTurn(ctx, sessionID, turnID)
}

// SubmitFeedback records an explicit reward signal ("up"/"down") for a turn.
// A "down" with a correction also stores a do_not_repeat fact that future
// turns surface as negative guidance. When explicit signals are disabled in
// config, it is a no-op so the widget can call it unconditionally.
func (o *Orchestrator) SubmitFeedback(ctx context.Context, sessionID string, turnID int64, signal, correction string) error {
	if !o.cfg.Feedback.WithDefaults().ExplicitSignalsEnabled {
		return nil
	}
	if sessionID == "" {
		var err error
		sessionID, err = o.ActiveSession(ctx)
		if err != nil {
			return err
		}
	}
	var turnPtr *int64
	if turnID > 0 {
		turnPtr = &turnID
	}
	if err := o.chat.AddFeedback(ctx, sessionID, turnPtr, signal, correction); err != nil {
		return err
	}
	// Best-effort: drain the learning event AddFeedback just queued so any
	// promoted memory is available immediately. A drain error never masks the
	// successful feedback write.
	_, _ = o.drainLearningQueue(ctx, 20)
	return nil
}

func (o *Orchestrator) ContextUsage(ctx context.Context, sessionID string) (chatstore.Usage, error) {
	if sessionID == "" {
		var err error
		sessionID, err = o.ActiveSession(ctx)
		if err != nil {
			return chatstore.Usage{}, err
		}
	}
	snap := o.snapshot()
	usage, err := o.chat.Usage(ctx, sessionID, snap.entry.Key, snap.entry.Model, o.contextWindow())
	if err != nil {
		return usage, err
	}
	// Add the fixed per-request prompt overhead that the chat-turn sum ignores:
	// the Ask system prompt plus the negative-guidance block are sent on every
	// turn but never stored as chat_turns. Without this, usage (and the
	// auto-compact threshold that reads it) understates how full the context
	// window actually is.
	overhead := estimateTextTokens(o.systemPrompt(prompts.RoleAsk))
	overhead += estimateTextTokens(o.negativeGuidanceBlock(ctx))
	usage.UsedTokens += overhead
	if usage.ContextWindow > 0 {
		usage.Percent = (usage.UsedTokens * 100) / usage.ContextWindow
	}
	return usage, nil
}

func responseEvent(sessionID, text string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = text
	return ev
}
