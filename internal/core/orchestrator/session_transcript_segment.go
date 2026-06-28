package orchestrator

import (
	"context"
	"sort"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// TranscriptSegmentMeta describes one loaded compaction segment and how to fetch older history.
type TranscriptSegmentMeta struct {
	SessionID         string `json:"session_id"`
	SegmentCheckpoint int    `json:"segment_checkpoint"` // -1 latest tail, 0 pre-first, >0 anchored at checkpoint
	HasOlder          bool   `json:"has_older"`
	OlderCheckpoint   int    `json:"older_checkpoint"` // next segment_checkpoint when scrolling up
	IsLatest          bool   `json:"is_latest"`
}

// SessionTranscriptSegment returns transcript entries for one compaction segment.
// segmentCheckpoint: -1 = latest active tail (default open), 0 = before first checkpoint, k>0 = era at checkpoint k.
func (o *Orchestrator) SessionTranscriptSegment(ctx context.Context, sessionID string, segmentCheckpoint int) ([]bridge.TranscriptEntry, TranscriptSegmentMeta, error) {
	meta := TranscriptSegmentMeta{SessionID: sessionID, SegmentCheckpoint: segmentCheckpoint}
	if o.chat == nil {
		return nil, meta, nil
	}
	turns, err := o.chat.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		return nil, meta, err
	}
	checkpoints, _ := o.chat.Checkpoints(ctx, sessionID)
	slice, meta := sliceTurnsForSegment(turns, checkpoints, segmentCheckpoint)
	meta.SessionID = sessionID
	events, _ := o.readSessionProgressEvents(sessionID)
	events = filterProgressEventsForTurns(events, slice)
	for _, record := range o.sessionTaskRecordsFromDisk(sessionID) {
		ev := taskUpdateEvent(record.SessionID, record)
		if ev.Kind == "" {
			continue
		}
		ev.At = taskTimelineAt(record)
		if eventInTurnWindow(ev, slice) {
			events = append(events, ev)
		}
	}
	return mergeTranscriptItems(slice, events), meta, nil
}

func sliceTurnsForSegment(turns []chatstore.Turn, checkpoints []chatstore.Checkpoint, segmentCheckpoint int) ([]chatstore.Turn, TranscriptSegmentMeta) {
	meta := TranscriptSegmentMeta{SegmentCheckpoint: segmentCheckpoint}
	if len(turns) == 0 {
		return nil, meta
	}
	sort.Slice(turns, func(i, j int) bool { return turns[i].Seq < turns[j].Seq })
	if len(checkpoints) == 0 {
		meta.IsLatest = true
		return append([]chatstore.Turn(nil), turns...), meta
	}
	sort.Slice(checkpoints, func(i, j int) bool { return checkpoints[i].Index < checkpoints[j].Index })
	latest := checkpoints[len(checkpoints)-1]
	_ = turnSeqByID(turns, latest.SummaryTurnID)

	if segmentCheckpoint < 0 {
		meta.IsLatest = true
		meta.SegmentCheckpoint = -1
		startSeq := turnSeqByID(turns, latest.TailStartTurnID)
		if startSeq == 0 {
			for _, t := range turns {
				if t.IncludedInContext && (startSeq == 0 || t.Seq < startSeq) {
					startSeq = t.Seq
				}
			}
		}
		out := turnsWithSeqGE(turns, startSeq)
		meta.HasOlder, meta.OlderCheckpoint = olderSegmentHint(checkpoints, latest.Index)
		return out, meta
	}
	if segmentCheckpoint == 0 {
		out := turnsWithSeqLT(turns, checkpoints[0].SummaryTurnID)
		meta.HasOlder = false
		meta.OlderCheckpoint = 0
		return out, meta
	}
	ck := findCheckpoint(checkpoints, segmentCheckpoint)
	if ck == nil {
		meta.IsLatest = true
		startSeq := turnSeqByID(turns, latest.TailStartTurnID)
		return turnsWithSeqGE(turns, startSeq), meta
	}
	startSeq := turnSeqByID(turns, ck.SummaryTurnID)
	endExclusive := 0
	if next := findCheckpoint(checkpoints, segmentCheckpoint+1); next != nil {
		endExclusive = turnSeqByID(turns, next.SummaryTurnID)
	} else {
		endExclusive = turnSeqByID(turns, latest.SummaryTurnID)
	}
	out := turnsWithSeqRange(turns, startSeq, endExclusive)
	meta.HasOlder, meta.OlderCheckpoint = olderForAnchor(checkpoints, segmentCheckpoint)
	return out, meta
}

func olderSegmentHint(checkpoints []chatstore.Checkpoint, latestIndex int) (bool, int) {
	if latestIndex < 1 {
		return false, 0
	}
	if latestIndex == 1 {
		return true, 0
	}
	return true, latestIndex - 1
}

func olderForAnchor(checkpoints []chatstore.Checkpoint, anchor int) (bool, int) {
	if anchor <= 1 {
		return true, 0
	}
	return true, anchor - 1
}

func findCheckpoint(checkpoints []chatstore.Checkpoint, index int) *chatstore.Checkpoint {
	for i := range checkpoints {
		if checkpoints[i].Index == index {
			return &checkpoints[i]
		}
	}
	return nil
}

func turnSeqByID(turns []chatstore.Turn, id int64) int {
	for _, t := range turns {
		if t.ID == id {
			return t.Seq
		}
	}
	return 0
}

func turnsWithSeqGE(turns []chatstore.Turn, minSeq int) []chatstore.Turn {
	var out []chatstore.Turn
	for _, t := range turns {
		if t.Seq >= minSeq {
			out = append(out, t)
		}
	}
	return out
}

func turnsWithSeqLT(turns []chatstore.Turn, beforeSummaryID int64) []chatstore.Turn {
	maxSeq := turnSeqByID(turns, beforeSummaryID)
	var out []chatstore.Turn
	for _, t := range turns {
		if t.Seq < maxSeq {
			out = append(out, t)
		}
	}
	return out
}

func turnsWithSeqRange(turns []chatstore.Turn, minSeq, endExclusive int) []chatstore.Turn {
	var out []chatstore.Turn
	for _, t := range turns {
		if t.Seq >= minSeq && t.Seq < endExclusive {
			out = append(out, t)
		}
	}
	return out
}

func filterProgressEventsForTurns(events []bridge.StreamEvent, turns []chatstore.Turn) []bridge.StreamEvent {
	if len(turns) == 0 {
		return nil
	}
	gens := make(map[string]struct{})
	var minAt, maxAt = turns[0].CreatedAt, turns[0].CreatedAt
	for _, t := range turns {
		if t.GenerationID != "" {
			gens[t.GenerationID] = struct{}{}
		}
		if t.CreatedAt.Before(minAt) {
			minAt = t.CreatedAt
		}
		if t.CreatedAt.After(maxAt) {
			maxAt = t.CreatedAt
		}
	}
	var out []bridge.StreamEvent
	for _, ev := range events {
		if eventInTurnWindow(ev, turns) {
			out = append(out, ev)
		}
	}
	return out
}

func eventInTurnWindow(ev bridge.StreamEvent, turns []chatstore.Turn) bool {
	if len(turns) == 0 {
		return false
	}
	if ev.GenerationID != "" {
		for _, t := range turns {
			if t.GenerationID == ev.GenerationID {
				return true
			}
		}
	}
	var minAt, maxAt = turns[0].CreatedAt, turns[0].CreatedAt
	for _, t := range turns {
		if t.CreatedAt.Before(minAt) {
			minAt = t.CreatedAt
		}
		if t.CreatedAt.After(maxAt) {
			maxAt = t.CreatedAt
		}
	}
	if ev.At.IsZero() {
		return false
	}
	return !ev.At.Before(minAt) && !ev.At.After(maxAt)
}
