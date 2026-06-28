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

// handleSlash runs a registered slash command. It returns a non-empty session id
// when the active chat session changed (e.g. /reset, /clear).
func (o *Orchestrator) handleSlash(ctx context.Context, out chan<- bridge.StreamEvent, sessionID, id, message string) string {
	switch id {
	case "settings":
		o.handleSettings(ctx, out, sessionID, message)
	case "compaction":
		count, err := o.compactActiveSession(ctx, sessionID, "manual")
		if err != nil {
			o.emitSlash(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return ""
		}
		o.emitSlash(ctx, out, sessionID, responseEvent(sessionID, fmt.Sprintf("Compaction complete. %d older turns summarized.", count)))
	case "clear":
		if err := o.chat.ClearSession(ctx, sessionID); err != nil {
			o.emitSlash(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return ""
		}
		if o.progress != nil {
			o.progress.Close(sessionID)
		}
		return sessionID
	case "reset":
		snap := o.snapshot()
		newID, err := o.chat.Reset(ctx, snap.entry.Key, snap.entry.Model)
		if err != nil {
			o.emitSlash(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return ""
		}
		o.inheritWorkspace(sessionID, newID)
		if o.progress != nil {
			o.progress.Close(sessionID)
		}
		return newID
	case "model":
		o.handleModel(ctx, out, sessionID, message)
	case "thinking":
		o.handleThinking(ctx, out, sessionID, message)
	default:
		o.emitSlash(ctx, out, sessionID, settingsEvent(sessionID, id))
	}
	return ""
}

func (o *Orchestrator) compactActiveSession(ctx context.Context, sessionID, reason string) (int, error) {
	if o.snapshot().cfg.Orchestrator.WithDefaults().Compaction.UseCheckpointsEnabled() {
		res, ok, err := o.runCompactSession(ctx, o.snapshot(), nil, sessionID, reason)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, nil
		}
		return res.CompactedTurns, nil
	}
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
	oldID, _ := o.ActiveSession(ctx)
	snap := o.snapshot()
	newID, err := o.chat.Reset(ctx, snap.entry.Key, snap.entry.Model)
	if err != nil {
		return "", err
	}
	o.inheritWorkspace(oldID, newID)
	return newID, nil
}

// DeleteSession removes a chat room. When the active room is deleted, another
// recent session is activated or a fresh one is created. The second return
// value is true when the caller should treat the result as a session reset
// (new empty active room).
func (o *Orchestrator) DeleteSession(ctx context.Context, sessionID string) (string, bool, error) {
	if sessionID == "" {
		return "", false, fmt.Errorf("session_id is required")
	}
	active, err := o.ActiveSession(ctx)
	if err != nil {
		return "", false, err
	}
	wasActive := active == sessionID
	// Deletion is a lifecycle barrier. Cancel the foreground generation and all
	// child actors first, then wait for their finalizers so none can recreate the
	// session directory after the store removes it.
	_, _ = o.Stop(sessionID, "all", "")
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for len(o.tasksForSession(sessionID)) > 0 {
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-deadline.C:
			return "", false, fmt.Errorf("timed out stopping actors for session %s", sessionID)
		case <-time.After(10 * time.Millisecond):
		}
	}
	o.purgeSessionTasks(sessionID)
	if err := o.chat.DeleteSession(ctx, sessionID); err != nil {
		return "", false, err
	}
	if o.progress != nil {
		o.progress.Close(sessionID)
	}
	if !wasActive {
		return active, false, nil
	}
	sessions, err := o.ListSessions(ctx, 20)
	if err != nil {
		return "", false, err
	}
	for _, summary := range sessions {
		if summary.ID == sessionID {
			continue
		}
		if err := o.chat.Activate(ctx, summary.ID); err != nil {
			continue
		}
		return summary.ID, false, nil
	}
	snap := o.snapshot()
	newID, err := o.chat.Reset(ctx, snap.entry.Key, snap.entry.Model)
	if err != nil {
		return "", false, err
	}
	return newID, true, nil
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
	contextWindow := o.contextWindow()
	usage, err := o.chat.Usage(ctx, sessionID, snap.entry.Key, snap.entry.Model, contextWindow)
	if err != nil {
		// Persist the real context window even when the turn scan fails so the
		// UI's context pill never degrades to "0/0" (a zero Usage looked like an
		// uninitialised counter on widget open). The used-token count is
		// unknown here, but the window itself is independent of the chat store.
		usage.ContextWindow = contextWindow
		usage.SessionID = sessionID
		usage.Provider = snap.entry.Key
		usage.Model = snap.entry.Model
		return usage, err
	}
	// Defence in depth: if the store returned a zero window for any reason
	// (e.g. a future code path forgets to pass it through), fall back to the
	// orchestrator's resolved window so the UI pill is never 0/0.
	if usage.ContextWindow <= 0 {
		usage.ContextWindow = contextWindow
	}
	// Recompute from turn bodies: TokenEstimate on paste may have counted raw
	// base64 as text tokens before strip-aware accounting existed.
	if turns, turnErr := o.chat.ActiveTurns(ctx, sessionID, false); turnErr == nil {
		used := 0
		for _, t := range turns {
			if t.IncludedInContext {
				used += estimateContentTokens(t.Content)
			}
		}
		usage.UsedTokens = used
	}
	// Add the fixed per-request prompt overhead that the chat-turn sum ignores:
	// the Ask system prompt, runtime context block, negative guidance, prefetch
	// and skills blocks are sent on every turn but never stored as chat_turns.
	// Without this, usage (and the compaction thresholds that read it) understate
	// how full the context window actually is, so the 5% headroom force-trigger
	// can fire too late (after a provider 400). The latest user message is needed
	// to size the prefetch/skills blocks accurately; best-effort (empty when no
	// user turn is found).
	userMsg := o.latestUserMessageContent(ctx, sessionID)
	usage.UsedTokens += o.estimatePerTurnOverhead(ctx, sessionID, userMsg)
	if usage.ContextWindow > 0 {
		usage.Percent = (usage.UsedTokens * 100) / usage.ContextWindow
	}
	return usage, nil
}

// estimatePerTurnOverhead sums the rough token cost of the per-request system
// blocks the orchestrator injects on every Ask turn but never persists as
// chat_turns: the Ask system prompt (persona-wrapped), the runtime context
// block, negative guidance, the prefetch packet, and the skills block. Keeping
// this in one place and reusing it from both ContextUsage and the mid-run
// headroom check prevents the two from drifting (a common cause of the
// force-trigger firing after, not before, a provider overflow).
func (o *Orchestrator) estimatePerTurnOverhead(ctx context.Context, sessionID, userMsg string) int {
	stripped, _ := stripAttachmentPayloads(userMsg)
	overhead := estimateTextTokens(o.systemPrompt(prompts.RoleAsk))
	overhead += estimateTextTokens(o.runtimeContextMessage(sessionID).Content)
	overhead += estimateTextTokens(o.negativeGuidanceBlock(ctx))
	overhead += estimateTextTokens(o.prefetchBlock(ctx, sessionID, stripped))
	overhead += estimateTextTokens(o.skillsBlock(ctx, stripped))
	return overhead
}

// latestUserMessageContent returns the content of the most recent persisted
// user turn for a session, or "" when there is none / the store is unavailable.
// It is used to size the prefetch/skills overhead blocks accurately.
func (o *Orchestrator) latestUserMessageContent(ctx context.Context, sessionID string) string {
	if o.chat == nil {
		return ""
	}
	turns, err := o.chat.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return ""
	}
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "user" {
			return turns[i].Content
		}
	}
	return ""
}

func responseEvent(sessionID, text string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = text
	return ev
}
