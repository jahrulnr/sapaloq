package orchestrator

import (
	"fmt"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// TranscriptCoalescer merges raw stream events into display-ready transcript
// entries for one generation (live run or task inspect replay).
type TranscriptCoalescer struct {
	generationID string
	entries      []bridge.TranscriptEntry
	textBuf      strings.Builder
	thinkBuf     strings.Builder
	nextID       int
	changed      bool
}

func NewTranscriptCoalescer(generationID string) *TranscriptCoalescer {
	return &TranscriptCoalescer{generationID: generationID}
}

func (c *TranscriptCoalescer) Entries() []bridge.TranscriptEntry {
	out := make([]bridge.TranscriptEntry, len(c.entries))
	copy(out, c.entries)
	return out
}

// EntriesWithPending includes in-flight response/thinking buffers as stable
// transcript rows so the widget can patch streaming text before flushText().
func (c *TranscriptCoalescer) EntriesWithPending() []bridge.TranscriptEntry {
	out := c.Entries()
	if text := c.textBuf.String(); strings.TrimSpace(text) != "" {
		out = append(out, bridge.TranscriptEntry{
			ID:           c.pendingID("text"),
			Kind:         bridge.TranscriptText,
			GenerationID: c.generationID,
			At:           time.Now().UTC(),
			Text:         text,
		})
	}
	if text := c.thinkBuf.String(); strings.TrimSpace(text) != "" {
		out = append(out, bridge.TranscriptEntry{
			ID:           c.pendingID("thinking"),
			Kind:         bridge.TranscriptThinking,
			GenerationID: c.generationID,
			At:           time.Now().UTC(),
			Text:         text,
		})
	}
	return out
}

func (c *TranscriptCoalescer) pendingID(kind string) string {
	if c.generationID == "" {
		return "pending-" + kind
	}
	return c.generationID + "-pending-" + kind
}

func (c *TranscriptCoalescer) Apply(ev bridge.StreamEvent) bool {
	c.changed = false
	if ev.GenerationID != "" {
		c.generationID = ev.GenerationID
	}
	switch ev.Kind {
	case bridge.EventResponseDelta:
		c.flushThinking()
		c.textBuf.WriteString(ev.Delta)
		c.changed = true
	case bridge.EventThinkingDelta:
		c.flushText()
		c.thinkBuf.WriteString(ev.Delta)
		c.changed = true
	case bridge.EventToolCall:
		c.flushText()
		c.flushThinking()
		if ev.ToolCall != nil {
			c.appendTool(ev.At, ev.ToolCall.ID, ev.ToolCall.Name, string(ev.ToolCall.Arguments), "", "")
		}
	case bridge.EventToolUpdate:
		c.flushText()
		c.flushThinking()
		c.completeTool(ev)
	case bridge.EventTurnBoundary, bridge.EventTaskUpdate:
		c.flushText()
		c.flushThinking()
		if ev.Kind == bridge.EventTaskUpdate {
			c.appendTask(ev)
		}
	case bridge.EventStatus:
		if ev.Status != "" && ev.Status != "working" {
			c.flushText()
			c.flushThinking()
			c.appendStatus(ev)
		}
	case bridge.EventError:
		c.flushText()
		c.flushThinking()
		c.entries = append(c.entries, bridge.TranscriptEntry{
			ID:           c.newID("error"),
			Kind:         bridge.TranscriptError,
			GenerationID: c.generationID,
			At:           ev.At,
			Text:         ev.Error,
		})
		c.changed = true
	case bridge.EventCheckpoint:
		c.flushText()
		c.flushThinking()
		c.entries = append(c.entries, bridge.TranscriptEntry{
			ID:               c.newID("checkpoint"),
			Kind:             bridge.TranscriptCheckpoint,
			GenerationID:     c.generationID,
			At:               ev.At,
			CheckpointIndex:  ev.CheckpointIndex,
			CheckpointReason: ev.CheckpointReason,
			Text:             ev.CheckpointSummary,
		})
		c.changed = true
	case bridge.EventDone:
		c.flushText()
		c.flushThinking()
		c.changed = true
	}
	return c.changed
}

// CoalesceEvents replays a raw event slice (history / task inspect).
func CoalesceEvents(generationID string, events []bridge.StreamEvent) []bridge.TranscriptEntry {
	c := NewTranscriptCoalescer(generationID)
	for _, ev := range events {
		c.Apply(ev)
	}
	c.flushText()
	c.flushThinking()
	return c.Entries()
}

func (c *TranscriptCoalescer) flushText() {
	text := c.textBuf.String()
	if strings.TrimSpace(text) != "" {
		c.entries = append(c.entries, bridge.TranscriptEntry{
			ID:           c.newID("text"),
			Kind:         bridge.TranscriptText,
			GenerationID: c.generationID,
			At:           time.Now().UTC(),
			Text:         text,
		})
		c.changed = true
	}
	c.textBuf.Reset()
}

func (c *TranscriptCoalescer) flushThinking() {
	text := c.thinkBuf.String()
	if strings.TrimSpace(text) != "" {
		c.entries = append(c.entries, bridge.TranscriptEntry{
			ID:           c.newID("thinking"),
			Kind:         bridge.TranscriptThinking,
			GenerationID: c.generationID,
			At:           time.Now().UTC(),
			Text:         text,
		})
		c.changed = true
	}
	c.thinkBuf.Reset()
}

func (c *TranscriptCoalescer) appendTool(at time.Time, id, name, args, result, status string) {
	c.entries = append(c.entries, bridge.TranscriptEntry{
		ID:           c.newID("tool"),
		Kind:         bridge.TranscriptTool,
		GenerationID: c.generationID,
		At:           at,
		ToolID:       id,
		ToolName:     name,
		ToolArgs:     args,
		ToolResult:   result,
		ToolStatus:   status,
	})
	c.changed = true
}

func (c *TranscriptCoalescer) completeTool(ev bridge.StreamEvent) {
	id, name, args := "", "", ""
	if ev.ToolCall != nil {
		id = ev.ToolCall.ID
		name = ev.ToolCall.Name
		args = string(ev.ToolCall.Arguments)
	}
	result := ev.ToolResult
	if result == "" {
		result = ev.Error
	}
	status := ev.Status
	if ev.Error != "" {
		status = "failed"
	} else if status == "" {
		status = "completed"
	}
	for i := len(c.entries) - 1; i >= 0; i-- {
		e := &c.entries[i]
		if e.Kind != bridge.TranscriptTool || e.ToolResult != "" {
			continue
		}
		if id != "" && e.ToolID == id {
			e.ToolResult = result
			e.ToolStatus = status
			c.changed = true
			return
		}
		if id == "" && e.ToolName == name {
			e.ToolResult = result
			e.ToolStatus = status
			c.changed = true
			return
		}
	}
	c.appendTool(ev.At, id, name, args, result, status)
}

func (c *TranscriptCoalescer) appendStatus(ev bridge.StreamEvent) {
	// Loop-steer hint for the model only (conversation.go). Persisted autopilot
	// turns are already skipped in SessionTranscript; dropping here keeps live
	// widget patches and task-inspect replay from flashing a fake user bubble.
	if isAutopilotNudge(ev.Status) {
		return
	}
	if isProgressStatus(ev.Status) {
		c.entries = append(c.entries, bridge.TranscriptEntry{
			ID:           c.newID("progress"),
			Kind:         bridge.TranscriptProgress,
			GenerationID: c.generationID,
			At:           ev.At,
			Label:        ev.Status,
			WaitSeconds:  ev.WaitSeconds,
		})
	} else {
		c.entries = append(c.entries, bridge.TranscriptEntry{
			ID:           c.newID("status"),
			Kind:         bridge.TranscriptStatus,
			GenerationID: c.generationID,
			At:           ev.At,
			Label:        ev.Status,
		})
	}
	c.changed = true
}

func (c *TranscriptCoalescer) appendTask(ev bridge.StreamEvent) {
	// Task lifecycle cards are orchestrator-chat UI only (session timeline +
	// live task_update bus). Sub-agent progress replay is for thinking/tools/
	// text; the monitor header already shows role/status.
	_ = ev
}

func (c *TranscriptCoalescer) newID(kind string) string {
	c.nextID++
	return fmt.Sprintf("%s-%s-%d", c.generationID, kind, c.nextID)
}

func isAutopilotNudge(status string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(status)), "continuing")
}

func isProgressStatus(status string) bool {
	switch status {
	case "waiting", "thinking", "compacting", "stopping":
		return true
	default:
		return false
	}
}
