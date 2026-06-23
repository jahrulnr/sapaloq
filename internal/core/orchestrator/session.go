package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/prompts"
	"github.com/jahrulnr/sapaloq/internal/skills"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// systemPrompt resolves the system prompt for a role via the prompt manager,
// falling back to the embedded default when the manager is nil (e.g. an
// Orchestrator constructed directly in tests). This is the single source of
// truth for every mode's system prompt.
//
// SapaLOQ's shared persona (persona.md) — its core character, applicable to
// every kind of work — is prepended to whatever role prompt is resolved, so
// ask/planner/agent/scribe (and any future role) all carry the same "how to
// carry yourself" baseline without duplicating it into each role file. The
// persona itself is never wrapped around itself, and a missing/empty persona
// is a no-op (the role prompt is returned unchanged).
func (o *Orchestrator) systemPrompt(role string) string {
	base := o.rolePrompt(role)
	if role == prompts.RolePersona {
		return base
	}
	persona := o.rolePrompt(prompts.RolePersona)
	if strings.TrimSpace(persona) == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return persona
	}
	return persona + "\n\n---\n\n" + base
}

// rolePrompt resolves a single role's prompt (on-disk override preferred, else
// embedded default) without applying the persona layer.
func (o *Orchestrator) rolePrompt(role string) string {
	if o != nil && o.prompts != nil {
		if p := o.prompts.Get(role); strings.TrimSpace(p) != "" {
			return p
		}
	}
	return prompts.Default(role)
}

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
	messages = append(messages, bridge.Message{Role: "system", Content: o.systemPrompt(prompts.RoleAsk)})
	messages = append(messages, o.runtimeContextMessage())
	if block := o.negativeGuidanceBlock(ctx); block != "" {
		messages = append(messages, bridge.Message{Role: "system", Content: block})
	}
	// Index-first prefetch (Context-SOP Fase 1): assemble a bounded memory
	// packet from companion.db and inject it as a system block so the model has
	// the right facts before acting — and, when confidence is high, a directive
	// not to explore the filesystem first. Best-effort: a low-confidence/empty
	// packet renders "" and is skipped.
	if block := o.prefetchBlock(ctx, sessionID, latestUserMessage); block != "" {
		messages = append(messages, bridge.Message{Role: "system", Content: block})
	}
	if block := o.skillsBlock(ctx, latestUserMessage); block != "" {
		messages = append(messages, bridge.Message{Role: "system", Content: block})
	}
	for _, turn := range turns {
		role := turn.Role
		// Thinking turns are persisted for the UI only — never replay reasoning
		// back into the model's context window.
		if role == "thinking" {
			continue
		}
		// "tool"/"error" turns keep their semantic role here; the wire layer
		// (wireRole) maps them to an API-accepted role at request-build time.
		// Centralizing the mapping there keeps live and replayed turns
		// consistent and lets a tool observation stay distinguishable from a
		// user request for as long as possible.
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

// prefetchBlock runs the index-first prefetch pipeline for a user message and
// renders its bounded system block. It logs one prefetch telemetry row per call
// (best-effort) so rule tuning has data. Returns "" when memory prefetch is
// disabled, the orchestrator has no store, or the packet has nothing to inject.
//
// This is the anti-forget anchor: the packet is assembled from companion.db,
// never the transcript, so it is identical before and after a compaction.
func (o *Orchestrator) prefetchBlock(ctx context.Context, sessionID, userMsg string) string {
	if o == nil || o.chat == nil {
		return ""
	}
	if !o.cfg.Memory.WithDefaults().PrefetchEnabled {
		return ""
	}
	start := time.Now()
	packet := o.prefetchContext(ctx, userMsg)
	block := packet.render()
	// Telemetry: deep_check_used is the inverse of the anti-deep-check decision
	// at assembly time (the actual tool loop may still escalate; that refinement
	// is left to the learning layer).
	_ = o.chat.LogPrefetch(ctx, chatstore.PrefetchTelemetry{
		SessionID:     sessionID,
		Intent:        packet.Intent,
		Confidence:    packet.Confidence,
		DeepCheckUsed: !packet.AntiDeepCheck,
		LatencyMS:     time.Since(start).Milliseconds(),
	})
	return block
}

// skillsBlock builds a short system block listing the skills relevant to the
// current user message. Selection is trigger-phrase first (fast, deterministic),
// then augmented by an FTS/keyword search over indexed skill bodies, deduped by
// id, sorted by priority, and capped by skills.maxLoadPerTurn. Each body is
// bounded by skills.maxBodyLines. Returns "" when disabled or nothing matches.
func (o *Orchestrator) skillsBlock(ctx context.Context, userMsg string) string {
	if o == nil {
		return ""
	}
	cfg := o.cfg.Skills.WithDefaults()
	if !cfg.Enabled {
		return ""
	}
	o.skillsMu.RLock()
	loaded := o.skills
	o.skillsMu.RUnlock()
	if len(loaded) == 0 {
		return ""
	}

	byID := make(map[string]skills.Skill, len(loaded))
	for _, sk := range loaded {
		byID[sk.ID] = sk
	}

	selected := make(map[string]skills.Skill)
	for _, sk := range skills.Match(loaded, userMsg) {
		selected[sk.ID] = sk
	}

	// Secondary signal: FTS/keyword search over indexed skill bodies. Map any
	// hit back to a loaded skill by id (first token of the stored content).
	if len(selected) < cfg.MaxLoadPerTurn && o.chat != nil && strings.TrimSpace(userMsg) != "" {
		if facts, err := o.chat.SearchFacts(ctx, userMsg, []string{"skill"}, cfg.MaxLoadPerTurn*3); err == nil {
			for _, f := range facts {
				id, _, ok := splitSkillFact(f.Content)
				if !ok {
					continue
				}
				if sk, ok := byID[id]; ok {
					selected[sk.ID] = sk
				}
			}
		}
	}
	if len(selected) == 0 {
		return ""
	}

	picks := make([]skills.Skill, 0, len(selected))
	for _, sk := range selected {
		picks = append(picks, sk)
	}
	picks = skills.SortByRelevance(picks, cfg.MaxLoadPerTurn)

	var b strings.Builder
	b.WriteString("Relevant skills (apply when appropriate):")
	for _, sk := range picks {
		b.WriteString("\n")
		b.WriteString(sk.Render(cfg.MaxBodyLines))
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
