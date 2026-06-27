package orchestrator

// session_timeline.go rebuilds the widget-visible tool/agent history that is
// not stored as chat turns. Tool call bubbles and task cards are emitted live
// over the event bus; on restart the chat transcript restores from SQLite but
// those UI elements would otherwise be missing until new work runs.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// SessionTimeline returns tool activity and task_update events for a chat session
// so the widget can interleave them with persisted turns on history restore.
// Terminal tasks are included here (unlike RecentTaskUpdates catch-up) because
// the task card is part of the transcript the user expects to see again.
func (o *Orchestrator) SessionTimeline(sessionID string) []bridge.StreamEvent {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	var out []bridge.StreamEvent
	if toolEvents, err := o.readSessionProgressToolCalls(sessionID); err == nil {
		out = append(out, toolEvents...)
	}
	for _, record := range o.sessionTaskRecordsFromDisk(sessionID) {
		ev := taskUpdateEvent(record.SessionID, record)
		if ev.Kind == "" {
			continue
		}
		ev.At = taskTimelineAt(record)
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := out[i].At, out[j].At
		if ti.Equal(tj) {
			return timelineKindOrder(out[i].Kind) < timelineKindOrder(out[j].Kind)
		}
		return ti.Before(tj)
	})
	return out
}

func timelineKindOrder(kind bridge.EventKind) int {
	switch kind {
	case bridge.EventToolCall:
		return 0
	case bridge.EventToolUpdate:
		return 1
	case bridge.EventTaskUpdate:
		return 2
	default:
		return 2
	}
}

func taskTimelineAt(record taskRecord) time.Time {
	switch record.Status {
	case "done", "failed", "stopped":
		if !record.UpdatedAt.IsZero() {
			return record.UpdatedAt
		}
	default:
		if !record.CreatedAt.IsZero() {
			return record.CreatedAt
		}
	}
	if !record.UpdatedAt.IsZero() {
		return record.UpdatedAt
	}
	return record.CreatedAt
}

func (o *Orchestrator) readSessionProgressToolCalls(sessionID string) ([]bridge.StreamEvent, error) {
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
		if ev.Kind == bridge.EventToolCall || ev.Kind == bridge.EventToolUpdate {
			out = append(out, ev)
		}
	}
	return out, sc.Err()
}

func (o *Orchestrator) sessionTaskRecordsFromDisk(sessionID string) []taskRecord {
	entries, err := os.ReadDir(o.tasksRoot())
	if err != nil {
		return nil
	}
	out := make([]taskRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, readErr := o.readTask(entry.Name())
		if readErr != nil {
			continue
		}
		if record.SessionID != sessionID {
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}
