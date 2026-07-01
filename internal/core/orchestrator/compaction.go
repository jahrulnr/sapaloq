package orchestrator

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/prompts"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// compaction.go owns checkpoint compaction: isolated summarization (Codex-style),
// tail anchoring, persistence, and in-memory rebuild helpers.

// compactionSummaryPrefix frames model-authored summaries (Codex-aligned).
const compactionSummaryPrefix = "[Checkpoint summary]"

// compactionPrompt returns the isolated summarization system prompt (no persona/rules/ask).
func (o *Orchestrator) compactionPrompt() string {
	return strings.TrimSpace(o.rolePrompt(prompts.RoleCompaction))
}

// runCompactSession is the orchestrator-driven compaction path: one tool-free
// LLM call to summarize archivable turns, then persist a checkpoint + anchored tail.
func (o *Orchestrator) runCompactSession(ctx context.Context, snap providerSnapshot, sink turnSink, sessionID, reason string) (chatstore.CheckpointResult, bool, error) {
	if o.chat == nil {
		return chatstore.CheckpointResult{}, false, nil
	}
	turns, err := o.chat.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return chatstore.CheckpointResult{}, false, err
	}
	cfg := snap.cfg.Orchestrator.WithDefaults().Compaction
	plan := computeTailPreserve(turns, cfg.KeepRecentTurns, cfg.PreservePrecedingUserTurn)
	if plan.tailStart <= 0 || len(plan.archiveTurnIDs) == 0 {
		return chatstore.CheckpointResult{}, false, nil
	}
	transcript := serializeTurnsForCompaction(turns[:plan.tailStart])
	summary, err := o.summarizeTranscript(ctx, snap, sessionID, transcript)
	if err != nil {
		return chatstore.CheckpointResult{}, false, err
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return chatstore.CheckpointResult{}, false, fmt.Errorf("compaction returned empty summary")
	}
	if !strings.HasPrefix(summary, compactionSummaryPrefix) {
		summary = compactionSummaryPrefix + "\n" + summary
	}
	rr := strings.TrimSpace(reason)
	if rr == "" {
		rr = "orchestrator"
	}
	pcfg := preserveCfg{keepRecentTurns: cfg.KeepRecentTurns, preservePrecedingUser: cfg.PreservePrecedingUserTurn}
	res, ok, err := o.createCheckpoint(ctx, sessionID, summary, rr, pcfg)
	if err != nil || !ok {
		return res, ok, err
	}
	o.emitCheckpoint(ctx, sink, sessionID, res, summary)
	return res, true, nil
}

func (o *Orchestrator) summarizeTranscript(ctx context.Context, snap providerSnapshot, sessionID, transcript string) (string, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return "", fmt.Errorf("nothing to summarize")
	}
	prompt := o.compactionPrompt()
	if prompt == "" {
		return "", fmt.Errorf("compaction prompt is not configured")
	}
	stream, err := snap.br.Complete(ctx, bridge.Request{
		SessionID: sessionID,
		Model:     snap.entry.Model,
		Messages: []bridge.Message{
			{Role: "system", Content: prompt},
			{Role: "user", Content: transcript},
		},
	})
	if err != nil {
		return "", err
	}
	return drainBridgeText(ctx, stream)
}

func drainBridgeText(ctx context.Context, stream <-chan bridge.StreamEvent) (string, error) {
	var b strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case ev, ok := <-stream:
			if !ok {
				return strings.TrimSpace(b.String()), nil
			}
			switch ev.Kind {
			case bridge.EventResponseDelta:
				b.WriteString(ev.Delta)
			case bridge.EventError:
				if ev.Error != "" {
					return "", fmt.Errorf("%s", ev.Error)
				}
			}
		}
	}
}

func serializeTurnsForCompaction(turns []chatstore.Turn) string {
	var b strings.Builder
	b.WriteString(prompts.GetInternal(prompts.KeyCompactionUserPrefix))
	b.WriteString("\n")
	for _, t := range turns {
		if t.Role == "thinking" || t.Role == "autopilot" || t.Role == "checkpoint" {
			continue
		}
		content := strings.TrimSpace(t.Content)
		if content == "" {
			continue
		}
		if len(content) > 12_000 {
			content = content[:12_000] + "\n…[truncated]"
		}
		b.WriteString("## ")
		b.WriteString(t.Role)
		b.WriteString("\n")
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	return b.String()
}

func serializeMessagesForCompaction(messages []bridge.Message) string {
	var b strings.Builder
	b.WriteString(prompts.GetInternal(prompts.KeyCompactionUserPrefix))
	b.WriteString("\n")
	for _, m := range messages {
		if m.Role == "thinking" || m.Role == "autopilot" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		if len(content) > 12_000 {
			content = content[:12_000] + "\n…[truncated]"
		}
		b.WriteString("## ")
		b.WriteString(m.Role)
		b.WriteString("\n")
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	return b.String()
}

// runSubAgentCompact summarizes in-memory sub-agent history via the same isolated
// compaction call, then applies compactMessagesWithSummary to the live slice.
func (o *Orchestrator) runSubAgentCompact(ctx context.Context, snap providerSnapshot, c *subAgentCompactCtx, reason string) error {
	if c == nil || c.messages == nil {
		return fmt.Errorf("nothing to compact")
	}
	msgs := *c.messages
	bodyStart := 0
	for i, m := range msgs {
		if m.Role != "system" {
			bodyStart = i
			break
		}
	}
	body := msgs[bodyStart:]
	if len(body) <= 6 {
		return fmt.Errorf("not enough history to compact")
	}
	preserve := snap.cfg.Orchestrator.WithDefaults().Compaction.PreserveRecentFraction
	if preserve <= 0 || preserve >= 1 {
		preserve = 0.30
	}
	keep := int(math.Ceil(float64(len(body)) * preserve))
	if keep < 4 {
		keep = 4
	}
	if keep >= len(body) {
		return fmt.Errorf("recent tail already covers context")
	}
	transcript := serializeMessagesForCompaction(body[:len(body)-keep])
	summary, err := o.summarizeTranscript(ctx, snap, c.parentSessionID, transcript)
	if err != nil {
		return err
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fmt.Errorf("compaction returned empty summary")
	}
	if !strings.HasPrefix(summary, compactionSummaryPrefix) {
		summary = compactionSummaryPrefix + "\n" + summary
	}
	pcfg := snap.cfg.Orchestrator.WithDefaults().Compaction
	res, persisted, persistErr := o.createCheckpoint(ctx, c.taskID, summary, reason, preserveCfg{
		keepRecentTurns:       pcfg.KeepRecentTurns,
		preservePrecedingUser: pcfg.PreservePrecedingUserTurn,
	})
	if persistErr != nil {
		return fmt.Errorf("persist actor checkpoint: %w", persistErr)
	}
	compacted := compactMessagesWithSummary(msgs, c.fallbackTask, summary, preserve)
	if len(compacted) >= len(msgs) {
		return fmt.Errorf("compaction did not shrink history")
	}
	*c.messages = compacted
	if persisted {
		c.checkpointIndex = res.Index
	} else {
		c.checkpointIndex++
	}
	rr := strings.TrimSpace(reason)
	if rr == "" {
		rr = "orchestrator"
	}
	if c.sink != nil {
		ev := bridge.NewEvent(bridge.EventCheckpoint)
		ev.SessionID = c.parentSessionID
		ev.TaskID = c.taskID
		ev.CheckpointIndex = c.checkpointIndex
		ev.CheckpointReason = rr
		ev.CheckpointSummary = summary
		c.sink.emit(ctx, ev)
	}
	return nil
}

// tailPreservePlan is the result of computeTailPreserve: the indices into the
// active-in-context turn list that form the post-checkpoint tail, plus the
// ids to archive and the tail-start id for audit.
type tailPreservePlan struct {
	// tailStart is the list index (into the input turns slice) of the first
	// turn to keep in context. turns[:tailStart] are archived.
	tailStart int
	// tailStartTurnID is the db id of turns[tailStart] (0 if none).
	tailStartTurnID int64
	// archiveTurnIDs are the ids of turns[:tailStart] to mark archived.
	archiveTurnIDs []int64
}

// computeTailPreserve decides which active-in-context turns survive as the
// post-checkpoint tail. The hard rule (plan 2a): the tail MUST always include
// the most recent assistant turn, so the model remembers what it just did. It
// may also include the user turn immediately before it (pairing the last
// exchange), and extends backward up to keepRecentTurns total - but never drops
// the anchored assistant turn.
//
// turns is the active-in-context turn list (oldest first), as returned by
// ActiveTurns(..., false). keepRecentTurns is the soft cap (default 4);
// preservePrecedingUser toggles the paired-user-turn inclusion.
//
// Returns a plan with tailStart == len(turns) when there is nothing worth
// archiving yet (caller should skip creating a checkpoint in that case).
func computeTailPreserve(turns []chatstore.Turn, keepRecentTurns int, preservePrecedingUser bool) tailPreservePlan {
	if keepRecentTurns < 1 {
		keepRecentTurns = 4
	}
	// Find the latest assistant turn (skip UI-only / internal roles and any
	// prior checkpoint markers - we never anchor on a checkpoint summary).
	lastAssistant := -1
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "assistant" {
			lastAssistant = i
			break
		}
	}
	if lastAssistant < 0 {
		// No assistant turn to anchor on: keep the soft cap of recent turns
		// (best effort) so we still compact, but the caller is encouraged to
		// steer the model to produce a turn first. Anchor at len-keepRecent.
		start := len(turns) - keepRecentTurns
		if start < 0 {
			start = 0
		}
		return buildTailPlan(turns, start)
	}
	// Anchor: the assistant turn (and optionally the user turn right before it)
	// must be in the tail.
	anchor := lastAssistant
	if preservePrecedingUser && anchor > 0 && turns[anchor-1].Role == "user" {
		anchor = anchor - 1
	}
	// Extend backward up to keepRecentTurns total, but never past the anchor.
	desiredStart := lastAssistant - keepRecentTurns + 1
	if desiredStart < 0 {
		desiredStart = 0
	}
	if desiredStart < anchor {
		desiredStart = anchor
	}
	return buildTailPlan(turns, desiredStart)
}

func buildTailPlan(turns []chatstore.Turn, start int) tailPreservePlan {
	if start < 0 {
		start = 0
	}
	if start > len(turns) {
		start = len(turns)
	}
	plan := tailPreservePlan{tailStart: start}
	for i := 0; i < start && i < len(turns); i++ {
		plan.archiveTurnIDs = append(plan.archiveTurnIDs, turns[i].ID)
	}
	if start < len(turns) {
		plan.tailStartTurnID = turns[start].ID
	}
	return plan
}

// createCheckpoint persists one LLM-authored checkpoint and returns the
// orchestrator-facing result (index + reason). The summary is the model's own
// structured markdown; the reason classifies the trigger
// ("model"|"force_headroom"|"force_overflow"|"manual"). The tail is computed
// from the current active-in-context turns via computeTailPreserve so the
// anchored-last-assistant-turn rule is enforced in one place. When there is
// nothing to archive (tail covers everything), it returns a zero result and
// no-op checkpoint - the caller should steer the model to keep working instead.
func (o *Orchestrator) createCheckpoint(ctx context.Context, sessionID, summary, reason string, cfg preserveCfg) (chatstore.CheckpointResult, bool, error) {
	if o.chat == nil {
		return chatstore.CheckpointResult{}, false, nil
	}
	turns, err := o.chat.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return chatstore.CheckpointResult{}, false, err
	}
	plan := computeTailPreserve(turns, cfg.keepRecentTurns, cfg.preservePrecedingUser)
	if plan.tailStart <= 0 || len(plan.archiveTurnIDs) == 0 {
		// Nothing to archive: the tail already covers every active turn. Do not
		// create an empty checkpoint - signal the caller to skip.
		return chatstore.CheckpointResult{}, false, nil
	}
	tail := chatstore.TailPolicy{
		ArchiveTurnIDs:  plan.archiveTurnIDs,
		TailStartTurnID: plan.tailStartTurnID,
	}
	res, err := o.chat.CreateCheckpoint(ctx, sessionID, summary, reason, tail, estimateTextTokens)
	if err != nil {
		return chatstore.CheckpointResult{}, false, err
	}
	return res, true, nil
}

// preserveCfg is the subset of CompactionConfig the checkpoint path reads.
type preserveCfg struct {
	keepRecentTurns       int
	preservePrecedingUser bool
}

// rebuildMessagesFromCheckpoint rebuilds the model-facing message slice after a
// checkpoint: the existing system prefix (all leading system blocks) + the
// latest checkpoint summary as a system message + the surviving tail turns.
// The caller is runTurnLoop, which holds the live cleanMessages slice; this
// helper produces the post-checkpoint slice that replaces it.
//
// NOTE: the contextMessages entry point (new user turn) instead reads from the
// store via ActiveTurns, which already respects included_in_context and
// replays the checkpoint marker turn (role=checkpoint) - see prompt.go.
func rebuildMessagesFromCheckpoint(systemPrefix []bridge.Message, ckpt chatstore.Checkpoint, tail []chatstore.Turn) []bridge.Message {
	out := make([]bridge.Message, 0, len(systemPrefix)+1+len(tail))
	out = append(out, systemPrefix...)
	if strings.TrimSpace(ckpt.Summary) != "" {
		out = append(out, bridge.Message{
			Role:    "system",
			Content: "[Checkpoint " + itoa(ckpt.Index) + " summary]\n" + ckpt.Summary,
		})
	}
	filtered := make([]chatstore.Turn, 0, len(tail))
	for _, t := range tail {
		if t.Role == "checkpoint" {
			continue
		}
		filtered = append(filtered, t)
	}
	out = append(out, actorTurnsToMessages(filtered)...)
	return out
}

// orchestratorFallbackCheckpoint persists a checkpoint with an orchestrator-
// authored summary when isolated LLM compaction fails or returns nothing to archive.
func (o *Orchestrator) orchestratorFallbackCheckpoint(ctx context.Context, sessionID, reason string) (chatstore.CheckpointResult, bool, error) {
	if o.chat == nil {
		return chatstore.CheckpointResult{}, false, nil
	}
	summary, err := o.buildHeuristicCheckpointSummary(ctx, sessionID, reason)
	if err != nil {
		return chatstore.CheckpointResult{}, false, err
	}
	cfg := o.snapshot().cfg.Orchestrator.WithDefaults().Compaction
	pcfg := preserveCfg{keepRecentTurns: cfg.KeepRecentTurns, preservePrecedingUser: cfg.PreservePrecedingUserTurn}
	return o.createCheckpoint(ctx, sessionID, summary, reason, pcfg)
}

// buildHeuristicCheckpointSummary rolls older active turns into a bounded
// markdown summary for orchestrator-driven compaction (fallback path).
func (o *Orchestrator) buildHeuristicCheckpointSummary(ctx context.Context, sessionID, reason string) (string, error) {
	turns, err := o.chat.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return "", err
	}
	if len(turns) <= 6 {
		return "", fmt.Errorf("not enough history to compact")
	}
	var b strings.Builder
	b.WriteString("## Checkpoint summary")
	if reason != "" {
		b.WriteString("\n\n_Auto-compacted by orchestrator (")
		b.WriteString(reason)
		b.WriteString(")_\n")
	}
	b.WriteString("\n\n")
	for _, t := range turns[:len(turns)-4] {
		if t.Role == "thinking" || t.Role == "autopilot" {
			continue
		}
		line := strings.TrimSpace(t.Content)
		if line == "" {
			continue
		}
		if len(line) > 400 {
			line = line[:400] + "…"
		}
		b.WriteString("- **")
		b.WriteString(t.Role)
		b.WriteString("**: ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// emitCheckpoint publishes a live EventCheckpoint to the widget sink + event bus
// so the UI can flush the current chat segment and insert a "Checkpoint n"
// divider. Best-effort: a closed ctx only skips the live emit, not the
// persistence (which already succeeded). sink may be nil (sub-agent path).
func (o *Orchestrator) emitCheckpoint(ctx context.Context, sink turnSink, sessionID string, res chatstore.CheckpointResult, summary string) {
	ev := bridge.NewEvent(bridge.EventCheckpoint)
	ev.SessionID = sessionID
	ev.CheckpointIndex = res.Index
	ev.CheckpointReason = res.Reason
	ev.CheckpointSummary = summary
	if sink != nil {
		sink.emit(ctx, ev)
	}
}

// forceCheckpoint runs isolated compaction (tool-free summarization call) when
// context headroom or overflow requires a checkpoint before the next Ask turn.
func (o *Orchestrator) forceCheckpoint(ctx context.Context, snap providerSnapshot, sink turnSink, sessionID, _, reason string, _ []bridge.Message) (chatstore.CheckpointResult, bool, error) {
	cfg := snap.cfg.Orchestrator.WithDefaults()
	maxRetries := cfg.Compaction.MaxForceRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return chatstore.CheckpointResult{}, false, ctx.Err()
		default:
		}
		res, ok, err := o.runCompactSession(ctx, snap, sink, sessionID, reason)
		if err != nil {
			return chatstore.CheckpointResult{}, false, err
		}
		if ok {
			return res, true, nil
		}
	}
	return o.orchestratorFallbackCheckpoint(ctx, sessionID, reason+"_fallback")
}

// latestCheckpointIndex returns the current highest checkpoint index for a
// session (0 when none). Best-effort: a store error yields 0.
func (o *Orchestrator) latestCheckpointIndex(ctx context.Context, sessionID string) (int, error) {
	if o.chat == nil {
		return 0, nil
	}
	ck, err := o.chat.LatestCheckpoint(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	return ck.Index, nil
}

// contextPercent returns the rough context usage percentage (0..100) of the
// in-flight message slice against the model's context window. It is the
// mid-run analog of ContextUsage.Percent and is used by the headroom force
// trigger so the trigger fires BEFORE a provider 400 rather than after.
func (o *Orchestrator) contextPercent(messages []bridge.Message, contextWindow int) int {
	if contextWindow <= 0 {
		return 0
	}
	used := estimateMessagesTokens(messages)
	if used <= 0 {
		return 0
	}
	return (used * 100) / contextWindow
}

// effectiveContextPercent is the headroom/compaction trigger percentage. It
// delegates to SessionContextLedger with the in-flight cleanMessages slice.
// Live messages already include injected system blocks, so overhead is not
// added again (avoids double-count vs the pre-restart pill).
func (o *Orchestrator) effectiveContextPercent(ctx context.Context, sessionID string, live []bridge.Message, contextWindow int) int {
	if contextWindow <= 0 {
		return 0
	}
	ledger, err := o.SessionContextLedger(ctx, sessionID, LedgerOptions{LiveMessages: live})
	if err != nil {
		return 0
	}
	return ledger.Percent
}

// shrinkContextForRun compacts the in-flight message slice before the next
// inference attempt. When tryCheckpoint is true it attempts LLM/orchestrator
// checkpoint compaction first; if that does not shrink the slice (or chat is
// unavailable) it falls back to the legacy in-memory heuristic so overflow
// recovery and tests without a store still work. Returns the (possibly
// unchanged) slice and true when the slice shrank.
func (o *Orchestrator) shrinkContextForRun(ctx context.Context, snap providerSnapshot, sink turnSink, sessionID, fallbackTask, reason string, cleanMessages []bridge.Message, tryCheckpoint bool) ([]bridge.Message, bool, error) {
	runtimeCfg := snap.cfg.Orchestrator.WithDefaults()
	preserve := runtimeCfg.Compaction.PreserveRecentFraction
	before := len(cleanMessages)

	if tryCheckpoint && runtimeCfg.Compaction.UseCheckpointsEnabled() {
		_, checkpointOK, ferr := o.forceCheckpoint(ctx, snap, sink, sessionID, fallbackTask, reason, cleanMessages)
		if ferr != nil {
			return cleanMessages, false, ferr
		}
		if checkpointOK {
			rebuilt, rerr := o.rebuildAfterCheckpoint(ctx, sessionID, cleanMessages)
			if rerr == nil && len(rebuilt) < before {
				return ensureConversationEndsWithUser(rebuilt), true, nil
			}
		}
	}

	compacted := compactConversationMessages(cleanMessages, fallbackTask, preserve)
	if len(compacted) < before {
		return compacted, true, nil
	}
	return cleanMessages, false, nil
}

// contextHeadroomReached reports whether the in-flight context has consumed
// (1 - headroomPercent) of the window - i.e. only headroomPercent remains. This
// is the moment to inject a forced compaction turn so the next inference does
// not overflow. headroomPercent is 0..1 (default 0.05 = 5% remaining).
func (o *Orchestrator) contextHeadroomReached(ctx context.Context, sessionID string, messages []bridge.Message, contextWindow int, headroomPercent float64) bool {
	if contextWindow <= 0 || headroomPercent <= 0 || headroomPercent >= 1 {
		return false
	}
	pct := o.effectiveContextPercent(ctx, sessionID, messages, contextWindow)
	if pct <= 0 {
		return false
	}
	threshold := int((1.0 - headroomPercent) * 100)
	return pct >= threshold
}

// rebuildAfterCheckpoint rebuilds the in-memory cleanMessages slice after a
// checkpoint was persisted: keep the leading system prefix from the live
// slice, then replace the body with the latest checkpoint summary + the
// surviving tail turns (read from the store, which already dropped archived
// rows via included_in_context=0). This keeps the live slice and the DB view
// in sync so the next inference turn sends exactly the checkpoint + tail.
func (o *Orchestrator) rebuildAfterCheckpoint(ctx context.Context, sessionID string, live []bridge.Message) ([]bridge.Message, error) {
	if o.chat == nil {
		return live, nil
	}
	// Preserve the leading system blocks (persona, runtime, negative guidance,
	// prefetch, skills) from the live slice - they are not stored as chat_turns
	// and must survive a checkpoint rebuild. Walk until the first non-system
	// message.
	prefix := []bridge.Message{}
	for _, m := range live {
		if m.Role != "system" {
			break
		}
		prefix = append(prefix, m)
	}
	ckpt, err := o.chat.LatestCheckpoint(ctx, sessionID)
	if err != nil {
		return live, err
	}
	turns, err := o.chat.ActiveTurns(ctx, sessionID, false)
	if err != nil {
		return live, err
	}
	return rebuildMessagesFromCheckpoint(prefix, ckpt, turns), nil
}

// subAgentCompactCtx wires durable compaction for background actors. Turns and
// checkpoints persist under state/tasks/{id}/ via the shared chat store; the
// live message slice owned by runTurnLoop is updated after createCheckpoint.
type subAgentCompactCtx struct {
	messages        *[]bridge.Message
	fallbackTask    string
	sink            turnSink
	taskID          string
	parentSessionID string
	checkpointIndex int
}

// compactMessagesWithSummary replaces the heuristic mid-run checkpoint body
// with a model-authored summary while preserving leading system blocks and a
// recent tail of conversation messages.
func compactMessagesWithSummary(messages []bridge.Message, originalTask, summary string, preserveRecentFraction float64) []bridge.Message {
	summary = strings.TrimSpace(summary)
	if summary == "" || len(messages) <= 6 {
		return messages
	}
	if preserveRecentFraction <= 0 || preserveRecentFraction >= 1 {
		preserveRecentFraction = 0.30
	}
	bodyStart := 0
	prefix := make([]bridge.Message, 0, 4)
	for i, m := range messages {
		if m.Role != "system" {
			bodyStart = i
			break
		}
		prefix = append(prefix, m)
	}
	body := messages[bodyStart:]
	keep := int(math.Ceil(float64(len(body)) * preserveRecentFraction))
	if keep < 4 {
		keep = 4
	}
	if keep >= len(body) {
		return messages
	}
	cut := len(body) - keep
	var checkpoint strings.Builder
	checkpoint.WriteString(prompts.GetInternal(prompts.KeyCompactionCheckpointHdr))
	checkpoint.WriteString("\n")
	checkpoint.WriteString(summary)
	checkpoint.WriteString("\n\nOriginal task: ")
	checkpoint.WriteString(truncateForCheckpoint(originalTask, 600))
	checkpoint.WriteString(prompts.GetInternal(prompts.KeyCompactionCheckpointResume))
	compacted := make([]bridge.Message, 0, len(prefix)+1+keep)
	compacted = append(compacted, prefix...)
	compacted = append(compacted, bridge.Message{Role: "system", Content: checkpoint.String()})
	compacted = append(compacted, body[cut:]...)
	return ensureConversationEndsWithUser(compacted)
}

func itoa(n int) string {
	// tiny dependency-free int->string to keep this file free of strconv import
	// churn; only used for the checkpoint summary header.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
