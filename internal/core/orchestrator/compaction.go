package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// compaction.go owns the LLM-driven checkpoint compaction path: deciding which
// turns survive as the post-checkpoint tail (anchored on the last assistant
// turn so the model never loses "what I just did"), persisting the checkpoint,
// and rebuilding the in-memory message slice from the latest checkpoint
// summary + that tail. It is the durable, model-authored counterpart to the
// deleted heuristic compactConversationMessages path.

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
	for _, t := range tail {
		if t.Role == "thinking" || t.Role == "autopilot" {
			continue
		}
		out = append(out, bridge.Message{Role: t.Role, Content: t.Content})
	}
	return out
}

// handleCompactSession is the dispatcher for the sapaloq_compact_session tool.
// The model supplies a structured markdown summary; the orchestrator persists a
// checkpoint (archiving pre-checkpoint turns for the UI, keeping the anchored
// tail + summary in context) and emits a live EventCheckpoint so the widget can
// insert a "Checkpoint n" divider. Returns a tool result that tells the model
// the checkpoint was created and it should continue from the live tail.
func (o *Orchestrator) handleCompactSession(ctx context.Context, snap providerSnapshot, sink turnSink, sessionID, summary, reason string) askToolResult {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return askToolResult{text: "Error: summary is required for sapaloq_compact_session.", handled: true}
	}
	cfg := snap.cfg.Orchestrator.WithDefaults().Compaction
	pcfg := preserveCfg{keepRecentTurns: cfg.KeepRecentTurns, preservePrecedingUser: cfg.PreservePrecedingUserTurn}
	res, ok, err := o.createCheckpoint(ctx, sessionID, summary, "model", pcfg)
	if err != nil {
		return askToolResult{text: "Compaction failed: " + err.Error(), handled: true}
	}
	if !ok {
		return askToolResult{text: "Nothing to compact yet - the recent tail already covers the active context. Continue your work; you do not need to compact again right now.", handled: true}
	}
	o.emitCheckpoint(ctx, sink, sessionID, res, summary)
	return askToolResult{text: fmt.Sprintf("Checkpoint %d created. The pre-checkpoint thread is archived for the user; your context now carries the summary + the most recent turns (including your last action). Continue the task from there - do not re-state what the summary already covers.", res.Index), handled: true}
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

// forceCheckpoint runs one blocking compaction turn: it injects a forced
// <sapaloq:autopilot> steering message telling the model to call
// sapaloq_compact_session with a full summary before any other work, then runs
// inference until the tool is called (or the retry budget is exhausted). On
// success the checkpoint is persisted and a live EventCheckpoint is emitted.
// It is the system-driven counterpart to the model-initiated tool, used by the
// headroom (95%) and overflow-400 triggers.
//
// Returns the created checkpoint result + true when a checkpoint was created,
// false when the model refused within the retry budget (caller surfaces an
// error suggesting /compaction or a shorter message - no silent heuristic
// fallback in v1).
func (o *Orchestrator) forceCheckpoint(ctx context.Context, snap providerSnapshot, sink turnSink, sessionID, fallbackTask, reason string, cleanMessages []bridge.Message) (chatstore.CheckpointResult, bool, error) {
	cfg := snap.cfg.Orchestrator.WithDefaults()
	maxRetries := cfg.Compaction.MaxForceRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	steering := sapaloqControlBody("Forced compaction: the conversation is too long for the context window. Before doing ANY other work, call `sapaloq_compact_session` with a full structured markdown summary (goals, decisions, open items, key facts) of the thread so far. Do NOT call `sapaloq_stop`. Do NOT repeat your last message in the summary - it is preserved in context separately. After the checkpoint succeeds, continue the task from the summary + recent tail.")
	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return chatstore.CheckpointResult{}, false, ctx.Err()
		default:
		}
		// Append the forced steering as the latest user turn and run ONE
		// inference turn offering the Ask tool surface (so the only useful
		// action is sapaloq_compact_session). We re-use the chat sink so the
		// user sees a brief "compacting" status, not the steering text.
		msgs := append([]bridge.Message{}, cleanMessages...)
		msgs = append(msgs, bridge.Message{Role: "user", Content: steering})
		// The compaction turn is bounded: runConversationActorSink drives the
		// loop until a terminal tool or the no-progress finish. The
		// sapaloq_compact_session handler persists the checkpoint and emits
		// EventCheckpoint inline; we detect success by reading the latest
		// checkpoint afterward.
		before, _ := o.latestCheckpointIndex(ctx, sessionID)
		var think strings.Builder
		_, _ = o.runConversationActorSink(ctx, snap, sink, sessionID, sessionID, fallbackTask, msgs, &think)
		after, _ := o.latestCheckpointIndex(ctx, sessionID)
		if after > before {
			ck, err := o.chat.LatestCheckpoint(ctx, sessionID)
			if err == nil {
				return chatstore.CheckpointResult{Index: ck.Index, SummaryTurnID: ck.SummaryTurnID, Reason: reason, CompactedTurns: ck.CompactedTurns, TailStartTurnID: ck.TailStartTurnID}, true, nil
			}
		}
	}
	return chatstore.CheckpointResult{}, false, nil
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

// contextHeadroomReached reports whether the in-flight context has consumed
// (1 - headroomPercent) of the window - i.e. only headroomPercent remains. This
// is the moment to inject a forced compaction turn so the next inference does
// not overflow. headroomPercent is 0..1 (default 0.05 = 5% remaining).
func (o *Orchestrator) contextHeadroomReached(messages []bridge.Message, contextWindow int, headroomPercent float64) bool {
	if contextWindow <= 0 || headroomPercent <= 0 || headroomPercent >= 1 {
		return false
	}
	pct := o.contextPercent(messages, contextWindow)
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
