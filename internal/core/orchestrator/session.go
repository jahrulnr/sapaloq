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
	snap := o.snapshot()
	if snap.entry.ContextWindow > 0 {
		return snap.entry.ContextWindow
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
	messages = append(messages, bridge.Message{Role: "system", Content: `You are SapaLOQ's Ask orchestrator. Use the active-session context below. Compacted summaries are authoritative; do not ask the user to repeat preserved context.
You can assess before delegating: call workspace_read_file {"path":"..."}, workspace_search {"pattern":"...","glob":"*.go"}, workspace_list_dir {"path":"."}, web_search {"query":"..."}, or web_fetch {"url":"..."} to gather facts yourself instead of guessing. Keep this light — for real work, delegate.
For work that needs investigation or a multi-step plan, call sapaloq_spawn_plan with {"task":"..."} (the planner reads/searches/researches read-only and writes a plan with acceptance criteria). For a clear execution request, call sapaloq_spawn_agent with {"task":"..."} (the agent reads, writes, and runs commands to finish the job; if a plan exists it executes that plan). These run asynchronously; do not pretend you executed their work yourself. Use sapaloq_get_task_status with {"task_id":"..."} when status is requested — it also surfaces any clarification a sub-agent needs. When a delegated task is awaiting_clarification, relay its question to the user; once they reply, call sapaloq_answer_clarification with {"task_id":"...","answer":"..."} to resume that same sub-agent with its accumulated context (do not re-spawn). Use sapaloq_wait with {"task_id":"...","seconds":2}; the backend waits for a task state change without repeatedly calling the model. Use sapaloq_stop with {"scope":"generation|task|all","task_id":"...","reason":"..."} when work should stop. Image input is available in Ask, planner, and agent modes when the selected model accepts vision.`})
	if block := o.negativeGuidanceBlock(ctx); block != "" {
		messages = append(messages, bridge.Message{Role: "system", Content: block})
	}
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

// negativeGuidanceBlock builds a short system block listing recent
// do_not_repeat facts the user flagged via 👎 feedback, bounded by
// config.feedback.maxNegativeSlicesPerTurn. Kept SHORT (like a t2i negative
// prompt) to protect the token budget. Returns "" when there is nothing to say.
func (o *Orchestrator) negativeGuidanceBlock(ctx context.Context) string {
	if o == nil || o.chat == nil {
		return ""
	}
	limit := o.cfg.Feedback.WithDefaults().MaxNegativeSlicesPerTurn
	facts, err := o.chat.RecentDoNotRepeat(ctx, limit)
	if err != nil || len(facts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Avoid repeating these mistakes the user flagged:")
	for _, f := range facts {
		b.WriteString("\n- ")
		b.WriteString(f.Content)
	}
	return b.String()
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
	return o.chat.AddFeedback(ctx, sessionID, turnPtr, signal, correction)
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
	return o.chat.Usage(ctx, sessionID, snap.entry.Key, snap.entry.Model, o.contextWindow())
}

func responseEvent(sessionID, text string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = text
	return ev
}
