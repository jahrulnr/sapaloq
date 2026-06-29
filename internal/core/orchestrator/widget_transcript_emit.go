package orchestrator

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

const deltaWidgetPatchMinInterval = 50 * time.Millisecond

// widgetPatchState holds throttled delta-transcript bookkeeping shared by
// foreground chat (activeRun) and background actors (subagentSink).
type widgetPatchState struct {
	lastDeltaPatch        time.Time
	deltaFlushScheduled   bool
	pendingTextUpsert     bool
	pendingThinkingUpsert bool
	pendingTextFlush      strings.Builder
	pendingThinkFlush     strings.Builder
}

// transcriptEmitOpts configures one coalesced transcript patch emission path.
// Foreground chat and sub-agent sinks both use emitCoalescedTranscript so delta
// throttle + snapshot boundaries stay identical (no duplicated widget logic).
type transcriptEmitOpts struct {
	sessionID          string
	actorID            string
	parentSessionID    string
	generationID       string
	coalescer          *TranscriptCoalescer
	patchState         *widgetPatchState
	patchMu            *sync.Mutex
	out                chan<- bridge.StreamEvent
	mergePersistedBase bool
	usageSessionID     string
}

func (o *Orchestrator) emitCoalescedTranscript(ctx context.Context, opts transcriptEmitOpts, ev bridge.StreamEvent) bool {
	if !widgetPatchKind(ev.Kind) {
		return true
	}
	if ev.Kind == bridge.EventResponseDelta || ev.Kind == bridge.EventThinkingDelta {
		return o.emitCoalescedTextDelta(ctx, opts, ev)
	}
	if opts.patchState != nil && opts.patchMu != nil {
		opts.patchMu.Lock()
		opts.patchState.resetPendingUpsert(ev.Kind)
		opts.patchMu.Unlock()
	}
	finished := ev.Kind == bridge.EventError || ev.Kind == bridge.EventDone
	entries := o.snapshotEntriesForEmit(ctx, opts, ev, finished)
	patch := bridge.SnapshotPatch(opts.sessionID, opts.generationID, entries, finished)
	patch.ActorID = opts.actorID
	patch.ParentSessionID = opts.parentSessionID
	if ev.Kind != bridge.EventResponseDelta && ev.Kind != bridge.EventThinkingDelta && opts.usageSessionID != "" && o.chat != nil {
		if u, err := o.ContextUsage(ctx, opts.usageSessionID); err == nil {
			patch.Usage = &bridge.TranscriptUsage{
				UsedTokens:    u.UsedTokens,
				ContextWindow: u.ContextWindow,
				Percent:       u.Percent,
				Provider:      u.Provider,
				Model:         u.Model,
			}
		}
	}
	patchEv := bridge.StreamEvent{
		Kind:              bridge.EventTranscript,
		SessionID:         opts.sessionID,
		ActorID:           opts.actorID,
		ParentSessionID:   opts.parentSessionID,
		GenerationID:      opts.generationID,
		Transcript:        &patch,
		At:                time.Now().UTC(),
	}
	return o.emitTranscriptPatch(ctx, opts.out, patchEv)
}

func (o *Orchestrator) snapshotEntriesForEmit(ctx context.Context, opts transcriptEmitOpts, ev bridge.StreamEvent, finished bool) []bridge.TranscriptEntry {
	if opts.coalescer == nil {
		return nil
	}
	if !opts.mergePersistedBase {
		return opts.coalescer.EntriesWithPending()
	}
	sessionID := opts.usageSessionID
	if finished || ev.Kind == bridge.EventTurnBoundary || ev.Kind == bridge.EventCheckpoint ||
		ev.Kind == bridge.EventToolUpdate || ev.Kind == bridge.EventToolCall {
		o.refreshActiveTranscriptBase(ctx, sessionID)
	}
	if finished {
		if live, err := o.LiveSessionTranscript(ctx, sessionID, opts.coalescer); err == nil && len(live) > 0 {
			return live
		}
		base := o.activeTranscriptBase(sessionID)
		if base == nil {
			return opts.coalescer.EntriesWithPending()
		}
		return mergeLiveTranscript(base, opts.coalescer.EntriesWithPending())
	}
	base := o.activeTranscriptBase(sessionID)
	if base == nil {
		entries, _ := o.LiveSessionTranscript(ctx, sessionID, opts.coalescer)
		return entries
	}
	return mergeLiveTranscript(base, opts.coalescer.EntriesWithPending())
}

func (o *Orchestrator) emitCoalescedTextDelta(ctx context.Context, opts transcriptEmitOpts, ev bridge.StreamEvent) bool {
	if opts.coalescer == nil || opts.patchState == nil || opts.patchMu == nil {
		return true
	}
	if opts.patchState.throttleDelta(opts.patchMu) {
		opts.patchState.accumulateFlush(opts.patchMu, ev)
		o.scheduleCoalescedDeltaFlush(ctx, opts)
		return true
	}
	opts.patchMu.Lock()
	ops := buildTextDeltaOps(opts.patchState, opts.coalescer, opts.generationID, ev.Kind, ev.Delta)
	opts.patchState.markDeltaPatch()
	opts.patchMu.Unlock()
	patch := bridge.DeltaPatch(opts.sessionID, opts.generationID, ops)
	patch.ActorID = opts.actorID
	patch.ParentSessionID = opts.parentSessionID
	patchEv := bridge.StreamEvent{
		Kind:              bridge.EventTranscript,
		SessionID:         opts.sessionID,
		ActorID:           opts.actorID,
		ParentSessionID:   opts.parentSessionID,
		GenerationID:      opts.generationID,
		Transcript:        &patch,
		At:                time.Now().UTC(),
	}
	return o.emitTranscriptPatch(ctx, opts.out, patchEv)
}

func (o *Orchestrator) scheduleCoalescedDeltaFlush(ctx context.Context, opts transcriptEmitOpts) {
	if opts.patchState == nil || opts.patchMu == nil {
		return
	}
	opts.patchMu.Lock()
	if opts.patchState.deltaFlushScheduled {
		opts.patchMu.Unlock()
		return
	}
	opts.patchState.deltaFlushScheduled = true
	opts.patchMu.Unlock()
	flushOpts := opts
	time.AfterFunc(deltaWidgetPatchMinInterval, func() {
		o.flushCoalescedDeltaPatch(ctx, flushOpts)
	})
}

func (o *Orchestrator) flushCoalescedDeltaPatch(ctx context.Context, opts transcriptEmitOpts) {
	if opts.patchState == nil || opts.patchMu == nil || opts.coalescer == nil {
		return
	}
	opts.patchMu.Lock()
	opts.patchState.deltaFlushScheduled = false
	textDelta := opts.patchState.pendingTextFlush.String()
	thinkDelta := opts.patchState.pendingThinkFlush.String()
	opts.patchState.pendingTextFlush.Reset()
	opts.patchState.pendingThinkFlush.Reset()
	coalescer := opts.coalescer
	genID := opts.generationID
	patchState := opts.patchState
	opts.patchMu.Unlock()
	var ops []bridge.TranscriptPatchOp
	if textDelta != "" {
		opts.patchMu.Lock()
		ops = append(ops, buildTextDeltaOps(patchState, coalescer, genID, bridge.EventResponseDelta, textDelta)...)
		opts.patchMu.Unlock()
	}
	if thinkDelta != "" {
		opts.patchMu.Lock()
		ops = append(ops, buildTextDeltaOps(patchState, coalescer, genID, bridge.EventThinkingDelta, thinkDelta)...)
		opts.patchMu.Unlock()
	}
	if len(ops) == 0 {
		return
	}
	opts.patchMu.Lock()
	patchState.markDeltaPatch()
	opts.patchMu.Unlock()
	patch := bridge.DeltaPatch(opts.sessionID, genID, ops)
	patch.ActorID = opts.actorID
	patch.ParentSessionID = opts.parentSessionID
	patchEv := bridge.StreamEvent{
		Kind:              bridge.EventTranscript,
		SessionID:         opts.sessionID,
		ActorID:           opts.actorID,
		ParentSessionID:   opts.parentSessionID,
		GenerationID:      genID,
		Transcript:        &patch,
		At:                time.Now().UTC(),
	}
	_ = o.emitTranscriptPatch(ctx, opts.out, patchEv)
}

func buildTextDeltaOps(ps *widgetPatchState, coalescer *TranscriptCoalescer, genID string, kind bridge.EventKind, delta string) []bridge.TranscriptPatchOp {
	var entryID string
	var entryKind bridge.TranscriptEntryKind
	var upsertSent *bool
	switch kind {
	case bridge.EventThinkingDelta:
		entryID = coalescer.PendingThinkingID()
		entryKind = bridge.TranscriptThinking
		upsertSent = &ps.pendingThinkingUpsert
	default:
		entryID = coalescer.PendingTextID()
		entryKind = bridge.TranscriptText
		upsertSent = &ps.pendingTextUpsert
	}
	var ops []bridge.TranscriptPatchOp
	if !*upsertSent {
		ops = append(ops, bridge.TranscriptPatchOp{
			Op: "upsert",
			Entry: bridge.TranscriptEntry{
				ID:           entryID,
				Kind:         entryKind,
				GenerationID: genID,
				At:           time.Now().UTC(),
			},
		})
		*upsertSent = true
	}
	if delta != "" {
		ops = append(ops, bridge.TranscriptPatchOp{
			Op:      "append_text",
			EntryID: entryID,
			Delta:   delta,
		})
	}
	return ops
}

func (ps *widgetPatchState) accumulateFlush(mu *sync.Mutex, ev bridge.StreamEvent) {
	mu.Lock()
	defer mu.Unlock()
	switch ev.Kind {
	case bridge.EventThinkingDelta:
		ps.pendingThinkFlush.WriteString(ev.Delta)
	default:
		ps.pendingTextFlush.WriteString(ev.Delta)
	}
}

func (ps *widgetPatchState) resetPendingUpsert(kind bridge.EventKind) {
	switch kind {
	case bridge.EventDone, bridge.EventError, bridge.EventTurnBoundary,
		bridge.EventToolCall, bridge.EventToolUpdate, bridge.EventCheckpoint,
		bridge.EventStatus, bridge.EventTaskUpdate:
	default:
		return
	}
	ps.pendingTextUpsert = false
	ps.pendingThinkingUpsert = false
	ps.pendingTextFlush.Reset()
	ps.pendingThinkFlush.Reset()
}

func (ps *widgetPatchState) throttleDelta(mu *sync.Mutex) bool {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	if ps.lastDeltaPatch.IsZero() || now.Sub(ps.lastDeltaPatch) >= deltaWidgetPatchMinInterval {
		return false
	}
	return true
}

func (ps *widgetPatchState) markDeltaPatch() {
	ps.lastDeltaPatch = time.Now()
	ps.deltaFlushScheduled = false
}

func (o *Orchestrator) emitTranscriptPatch(ctx context.Context, out chan<- bridge.StreamEvent, patchEv bridge.StreamEvent) bool {
	if o.bus != nil {
		o.bus.Publish(topicFor(bridge.EventTranscript), patchEv)
	}
	return o.sendOut(ctx, out, patchEv)
}

func widgetCoalesceKind(kind bridge.EventKind) bool {
	switch kind {
	case bridge.EventThinkingDelta, bridge.EventResponseDelta, bridge.EventToolCall,
		bridge.EventToolUpdate, bridge.EventStatus, bridge.EventTurnBoundary,
		bridge.EventTaskUpdate, bridge.EventError, bridge.EventCheckpoint, bridge.EventDone:
		return true
	default:
		return false
	}
}

func widgetPatchKind(kind bridge.EventKind) bool {
	switch kind {
	case bridge.EventThinkingDelta, bridge.EventResponseDelta, bridge.EventToolCall,
		bridge.EventToolUpdate, bridge.EventStatus, bridge.EventTurnBoundary,
		bridge.EventTaskUpdate, bridge.EventError, bridge.EventCheckpoint,
		bridge.EventDone, bridge.EventTranscript:
		return true
	default:
		return false
	}
}
