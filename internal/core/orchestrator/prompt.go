package orchestrator

// prompt.go is the single home for system-prompt resolution and the
// per-turn system-block builders that the orchestrator composes for both the
// chat (Ask) role and the sub-agent roles (planner / task-runner / scribe).
// Before this file existed, those pieces were scattered across session.go,
// runtime_context.go, and subagent.go, which made it hard to see — in one
// place — exactly what every model invocation actually sees as its system
// surface. Concentrating them here also keeps each builder next to its
// neighbours, so a new role, a new system block, or a new persona layer is
// added in one obvious spot.
//
// What lives here:
//
//   - Role / persona prompt resolution
//       systemPrompt, rolePrompt
//   - The runtime context block (paths, ROADMAP)
//       runtimeContextMessage, materializeRuntimeRoadmap
//   - The bounded per-turn system blocks
//       negativeGuidanceBlock, prefetchBlock, skillsBlock
//   - The two message-assembly entry points
//       contextMessages (Ask / chat)
//       buildSubAgentMessages (planner / task-runner / scribe)
//   - The small token-estimator helper that every block size check relies on
//       estimateTextTokens, defaultContextWindow, autoCompactPercent
//
// session.go keeps session lifecycle (ActiveSession, DeleteTurn, SubmitFeedback,
// ContextUsage, handleSlash, compactActiveSession). subagent.go keeps the
// sub-agent inference engine and the tool dispatcher. runtime_context.go is
// gone — its two functions moved here. Persona / role / runtime / negative /
// prefetch / skills blocks are no longer assembled in three different files.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/prompts"
	"github.com/jahrulnr/sapaloq/internal/skills"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// ---------------------------------------------------------------------------
// Role / persona resolution
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Token / context-size helpers
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Runtime context block (paths + workspace contract)
// ---------------------------------------------------------------------------

// runtimeContextMessage is the always-injected system block that names the
// on-disk paths SapaLOQ uses, so the model never has to guess where prompts /
// skills / state live. Cheap to build (no I/O) and bounded in size.
func (o *Orchestrator) runtimeContextMessage() bridge.Message {
	dirs := config.RuntimeDirs(o.snapshot().cfg)
	content := fmt.Sprintf(`[SapaLOQ runtime variables]
config_path=%s
data_path=%s
memory_path=%s
state_path=%s
workspace=%s
prompts_path=%s
skills_path=%s
vault_path=%s
run_path=%s
etc_path=%s
runtime_roadmap=%s

Use these paths instead of guessing. Relative tool paths resolve from the actor workspace.`,
		o.cfgPath, dirs.DataDir, dirs.MemoryDir, dirs.StateDir, dirs.WorkspaceDir,
		dirs.PromptsDir, dirs.SkillsDir, dirs.VaultDir, dirs.RunDir, dirs.EtcDir,
		filepath.Join(dirs.EtcDir, "ROADMAP.md"))
	return bridge.Message{Role: "system", Content: content}
}

// materializeRuntimeRoadmap writes the same content as runtimeContextMessage
// plus a short workspace contract to ROADMAP.md so the model (and humans
// debugging) can read it as a file. Idempotent and best-effort.
func (o *Orchestrator) materializeRuntimeRoadmap() {
	dirs := config.RuntimeDirs(o.snapshot().cfg)
	if dirs.EtcDir == "" {
		return
	}
	content := o.runtimeContextMessage().Content + `

[Workspace contract]
- Every actor starts at workspace unless it has a persisted cwd.
- Relative file and exec paths follow that actor cwd.
- cd persists for the same actor.
`
	if os.MkdirAll(dirs.EtcDir, 0o755) != nil {
		return
	}
	_ = writeFileAtomic(filepath.Join(dirs.EtcDir, "ROADMAP.md"), []byte(content), 0o600)
}

// ---------------------------------------------------------------------------
// Bounded per-turn system blocks
// ---------------------------------------------------------------------------

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
	b.WriteString("Relevant skills:")
	for _, sk := range picks {
		b.WriteString("\n")
		b.WriteString(sk.Render(cfg.MaxBodyLines))
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Message assembly
// ---------------------------------------------------------------------------

// contextMessages builds the full message slice for an Ask / chat turn:
//  1. The Ask system prompt (persona-wrapped via systemPrompt)
//  2. The runtime context block (paths, ROADMAP)
//  3. Bounded per-turn blocks: negative guidance, memory prefetch, skills
//  4. The persisted chat turns (excluding UI-only "thinking")
//  5. The latest user message, unless it is already the last persisted turn
//
// Auto-compaction is triggered here when the cached usage is at or above
// autoCompactPercent of the model's context window, so a long session gets
// trimmed before the next request instead of failing the call.
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

// buildSubAgentMessages assembles the system + user context for a sub-agent,
// including the user's original intent and (for agents) the handed-off plan
// with its acceptance criteria.
func (o *Orchestrator) buildSubAgentMessages(record *taskRecord) []bridge.Message {
	// Role system prompts are file-driven and replaceable (internal/prompts):
	// the on-disk copy is preferred, falling back to the embedded default. An
	// unknown role gets a minimal generic prompt.
	systemContent := o.systemPrompt(record.Role)
	if strings.TrimSpace(systemContent) == "" {
		systemContent = "You are a background SapaLOQ task agent. Use your tools, then return a concise final result."
	}

	messages := []bridge.Message{{Role: "system", Content: systemContent}}
	messages = append(messages, o.runtimeContextMessage())

	// Hand off the plan (goal + acceptance criteria) to the agent.
	if record.Role == "task-runner" && record.PlanTaskID != "" {
		if plan := o.readPlanMarkdown(record.PlanTaskID); plan != "" {
			messages = append(messages, bridge.Message{
				Role:    "system",
				Content: "Approved plan to execute (read it as authoritative; satisfy every item under ## Acceptance):\n\n" + plan,
			})
		}
	}

	messages = append(messages, bridge.Message{Role: "user", Content: record.Task})

	// Resume path: if the task has a persisted transcript (it was paused on a
	// clarification), replay it so the sub-agent continues with its prior
	// context. When an Answer is present, append it as the resume nudge.
	if len(record.Transcript) > 0 {
		for _, turn := range record.Transcript {
			role := turn.Role
			if role != "assistant" && role != "user" && role != "system" && role != "tool" {
				role = "user"
			}
			messages = append(messages, bridge.Message{Role: role, Content: turn.Content})
		}
	}
	if strings.TrimSpace(record.Answer) != "" {
		messages = append(messages, bridge.Message{
			Role:    "user",
			Content: "Answer to your clarification question: " + strings.TrimSpace(record.Answer) + "\nContinue the task using this answer.",
		})
	}
	return messages
}

// readPlanMarkdown loads the persisted plan for a task ID. It is path-safe by
// design: it only accepts a bare basename (no slashes) and silently returns ""
// if anything is off. Kept next to the message-assembly code that consumes it
// (buildSubAgentMessages) and the planner/agent tool path (read_plan) so any
// future change to plan storage has one obvious home.
func (o *Orchestrator) readPlanMarkdown(planTaskID string) string {
	if planTaskID == "" || filepath.Base(planTaskID) != planTaskID {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(o.taskDir(planTaskID), "plan.md"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
