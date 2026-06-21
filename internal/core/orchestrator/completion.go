package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// speakTaskCompletion closes the event-driven loop that the original bug left
// open: when a background sub-agent reaches a terminal state, the orchestrator
// must SPEAK the outcome into the conversation — not merely update a task card.
//
// Before this, `sapaloq_wait` would return "still in_progress" and the Ask
// generation would end; when the worker finished later, only a card updated and
// nobody re-entered the conversational thread, so the user never learned the
// task had completed. This injects a durable assistant turn into the task's
// session and republishes it as a streamed response event the `watch` op
// already forwards live.
//
// It is idempotent per task id and gated by completion.speakOnTerminal.
func (o *Orchestrator) speakTaskCompletion(sessionID string, record taskRecord) {
	if o == nil {
		return
	}
	if !taskTerminal(record.Status) {
		return
	}
	if !o.cfg.Orchestrator.WithDefaults().Completion.SpeakOnTerminal {
		return
	}
	o.spokenMu.Lock()
	if o.spokenTasks == nil {
		o.spokenTasks = make(map[string]struct{})
	}
	if _, already := o.spokenTasks[record.ID]; already {
		o.spokenMu.Unlock()
		return
	}
	o.spokenTasks[record.ID] = struct{}{}
	o.spokenMu.Unlock()

	text := spokenCompletionText(record)
	if text == "" {
		return
	}

	// Persist as an assistant turn so the completion survives a restart and
	// shows up in chat history exactly like a normal reply. Best-effort: a
	// missing session id or store error must not break the lifecycle push.
	if o.chat != nil && sessionID != "" {
		_ = o.chat.AppendTurn(context.Background(), sessionID, "assistant", text, estimateTextTokens(text))
	}

	// Republish as a streamed response so a connected widget hears it live via
	// the watch stream — this is the missing "speak" trigger.
	if o.bus != nil {
		ev := bridge.NewEvent(bridge.EventResponseDelta)
		ev.SessionID = sessionID
		ev.Delta = text
		o.bus.Publish(topicFor(bridge.EventResponseDelta), ev)
	}
	_ = o.progress.Append(record.ID, func() bridge.StreamEvent {
		ev := bridge.NewEvent(bridge.EventResponseDelta)
		ev.SessionID = sessionID
		ev.Delta = text
		return ev
	}())
}

// spokenCompletionText renders the human-facing chat line for a terminal task.
// Kept short and Indonesian-first to match the existing taskUpdateEvent voice.
func spokenCompletionText(record taskRecord) string {
	id := record.ID
	switch record.Status {
	case "done":
		summary := strings.TrimSpace(record.Result)
		if summary == "" {
			summary = "Task selesai."
		}
		return fmt.Sprintf("Task `%s` selesai ✅\n\n%s", id, summary)
	case "failed":
		reason := strings.TrimSpace(record.Error)
		if reason == "" {
			reason = "alasan tidak diketahui"
		}
		return fmt.Sprintf("Task `%s` gagal ❌: %s", id, reason)
	case "awaiting_clarification":
		q := strings.TrimSpace(record.Question)
		if q == "" {
			q = "butuh keputusan kamu."
		}
		return fmt.Sprintf("Task `%s` butuh keputusan 🤔: %s", id, q)
	case "stopped":
		return fmt.Sprintf("Task `%s` dihentikan ⏹️.", id)
	default:
		return ""
	}
}
