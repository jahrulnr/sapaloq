package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

var plannerSummaryRE = regexp.MustCompile(`^<!--sapaloq-planner-summary:([^>]+)-->\s*\n?([\s\S]*)$`)

type transcriptItem struct {
	at   time.Time
	seq  int
	kind string // turn | tool | task
	turn *chatstore.Turn
	ev   *bridge.StreamEvent
}

// SessionTranscript rebuilds the full widget transcript from SQLite turns and
// progress JSONL (tools + tasks), replacing the FE buildMergedTimeline path.
func (o *Orchestrator) SessionTranscript(ctx context.Context, sessionID string) ([]bridge.TranscriptEntry, error) {
	turns, err := o.chat.ActiveTurns(ctx, sessionID, true)
	if err != nil {
		return nil, err
	}
	events, _ := o.readSessionProgressEvents(sessionID)
	for _, record := range o.sessionTaskRecordsFromDisk(sessionID) {
		ev := taskUpdateEvent(record.SessionID, record)
		if ev.Kind == "" {
			continue
		}
		ev.At = taskTimelineAt(record)
		events = append(events, ev)
	}
	return mergeTranscriptItems(turns, events), nil
}

// LiveSessionTranscript returns persisted transcript plus in-flight coalescer
// entries for the active generation (assistant block not yet in SQLite).
func (o *Orchestrator) LiveSessionTranscript(ctx context.Context, sessionID string, live *TranscriptCoalescer) ([]bridge.TranscriptEntry, error) {
	base, err := o.SessionTranscript(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if live == nil {
		return base, nil
	}
	liveEntries := live.EntriesWithPending()
	if len(liveEntries) == 0 {
		return base, nil
	}
	return mergeLiveTranscript(base, liveEntries), nil
}

// mergeLiveTranscript overlays in-flight coalescer rows for the active generation
// onto the persisted transcript snapshot.
func mergeLiveTranscript(base []bridge.TranscriptEntry, live []bridge.TranscriptEntry) []bridge.TranscriptEntry {
	if len(live) == 0 {
		return base
	}
	genID := live[0].GenerationID
	if genID == "" {
		out := make([]bridge.TranscriptEntry, 0, len(base)+len(live))
		out = append(out, base...)
		out = append(out, live...)
		return out
	}
	filtered := make([]bridge.TranscriptEntry, 0, len(base))
	for _, e := range base {
		if e.GenerationID != genID || !isLiveGenerationEntryKind(e.Kind) {
			filtered = append(filtered, e)
		}
	}
	out := make([]bridge.TranscriptEntry, 0, len(filtered)+len(live))
	out = append(out, filtered...)
	out = append(out, live...)
	return out
}

func isLiveGenerationEntryKind(kind bridge.TranscriptEntryKind) bool {
	switch kind {
	case bridge.TranscriptText, bridge.TranscriptThinking, bridge.TranscriptTool,
		bridge.TranscriptStatus, bridge.TranscriptProgress, bridge.TranscriptError,
		bridge.TranscriptTask, bridge.TranscriptCheckpoint:
		return true
	default:
		return false
	}
}

func mergeTranscriptItems(turns []chatstore.Turn, events []bridge.StreamEvent) []bridge.TranscriptEntry {
	var items []transcriptItem
	for i := range turns {
		t := turns[i]
		if t.Role == "system" || t.Role == "tool" || t.Role == "autopilot" {
			continue
		}
		items = append(items, transcriptItem{at: t.CreatedAt, seq: t.Seq, kind: "turn", turn: &t})
	}
	for i := range events {
		ev := events[i]
		if ev.Kind == bridge.EventToolUpdate {
			if ev.ToolCall == nil || ev.ToolCall.ID == "" {
				continue
			}
			evCopy := events[i]
			items = append(items, transcriptItem{at: ev.At, kind: "event", ev: &evCopy})
			continue
		}
		if ev.Kind == bridge.EventTaskUpdate {
			evCopy := events[i]
			items = append(items, transcriptItem{at: ev.At, kind: "event", ev: &evCopy})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].at.Equal(items[j].at) {
			return items[i].at.Before(items[j].at)
		}
		if items[i].kind == "turn" && items[j].kind == "turn" {
			return items[i].seq < items[j].seq
		}
		return items[i].kind == "turn"
	})

	var out []bridge.TranscriptEntry
	for _, item := range items {
		if item.kind == "turn" {
			out = append(out, turnToEntry(*item.turn)...)
			continue
		}
		out = append(out, eventToEntry(*item.ev)...)
	}
	return out
}

func turnToEntry(t chatstore.Turn) []bridge.TranscriptEntry {
	archived := !t.IncludedInContext
	base := bridge.TranscriptEntry{
		ID:           fmt.Sprintf("turn-%d", t.ID),
		TurnID:       t.ID,
		Seq:          t.Seq,
		GenerationID: t.GenerationID,
		At:           t.CreatedAt,
		Archived:     archived,
	}
	switch t.Role {
	case "user":
		e := base
		e.Kind = bridge.TranscriptUser
		e.Text = stripAttachmentMeta(t.Content)
		return []bridge.TranscriptEntry{e}
	case "thinking":
		e := base
		e.Kind = bridge.TranscriptThinking
		e.Text = t.Content
		return []bridge.TranscriptEntry{e}
	case "checkpoint":
		e := base
		e.Kind = bridge.TranscriptCheckpoint
		e.CheckpointIndex = t.CheckpointIndex
		e.Text = t.Content
		return []bridge.TranscriptEntry{e}
	case "error":
		e := base
		e.Kind = bridge.TranscriptError
		e.Text = t.Content
		return []bridge.TranscriptEntry{e}
	case "assistant":
		if m := plannerSummaryRE.FindStringSubmatch(t.Content); len(m) == 3 {
			e := base
			e.Kind = bridge.TranscriptText
			e.Text = m[2]
			e.TaskID = m[1]
			e.TaskRole = "planner"
			return []bridge.TranscriptEntry{e}
		}
		e := base
		e.Kind = bridge.TranscriptText
		e.Text = stripCalledToolsForDisplay(t.Content)
		if strings.TrimSpace(e.Text) == "" {
			return nil
		}
		return []bridge.TranscriptEntry{e}
	default:
		return nil
	}
}

func eventToEntry(ev bridge.StreamEvent) []bridge.TranscriptEntry {
	switch ev.Kind {
	case bridge.EventToolUpdate:
		id, name, args := "", "", ""
		if ev.ToolCall != nil {
			id, name, args = ev.ToolCall.ID, ev.ToolCall.Name, string(ev.ToolCall.Arguments)
		}
		status := ev.Status
		if ev.Error != "" {
			status = "failed"
		} else if status == "" {
			status = "completed"
		}
		result := ev.ToolResult
		if result == "" {
			result = ev.Error
		}
		return []bridge.TranscriptEntry{{
			ID:           fmt.Sprintf("tool-%s", id),
			Kind:         bridge.TranscriptTool,
			GenerationID: ev.GenerationID,
			At:           ev.At,
			ToolID:       id,
			ToolName:     name,
			ToolArgs:     args,
			ToolResult:   result,
			ToolStatus:   status,
		}}
	case bridge.EventTaskUpdate:
		return []bridge.TranscriptEntry{{
			ID:           fmt.Sprintf("task-%s", ev.TaskID),
			Kind:         bridge.TranscriptTask,
			GenerationID: ev.GenerationID,
			At:           ev.At,
			TaskID:       ev.TaskID,
			TaskRole:     ev.TaskRole,
			TaskStatus:   ev.TaskStatus,
			Summary:      ev.Summary,
		}}
	default:
		return nil
	}
}

func stripAttachmentMeta(content string) string {
	return strings.TrimSpace(attachmentMetaRE.ReplaceAllString(content, ""))
}

func (o *Orchestrator) readSessionProgressEvents(sessionID string) ([]bridge.StreamEvent, error) {
	dir := o.progressDir()
	if dir == "" {
		return nil, fmt.Errorf("progress dir unavailable")
	}
	path := filepath.Join(dir, "orch-"+sessionID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []bridge.StreamEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev bridge.StreamEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Kind == bridge.EventToolCall || ev.Kind == bridge.EventToolUpdate || ev.Kind == bridge.EventTaskUpdate {
			out = append(out, ev)
		}
	}
	return out, sc.Err()
}
