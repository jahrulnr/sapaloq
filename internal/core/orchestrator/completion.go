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
// task had completed.
//
// The message is AUTHORED BY THE ORCHESTRATOR LLM, not copy-pasted from the
// sub-agent. The sub-agent's raw result/error is handed to the orchestrator as
// context and it announces the outcome in its own voice (so the wording varies
// naturally per model and reads like the assistant talking to the user, e.g.
// "sub-agent udah kelar, hasilnya ..."). The task CARD stays a terse status
// timeline (see taskUpdateEvent); this bubble is the single place the full
// summary lives — no more two redundant copies.
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
	spokenKey := record.ID + ":" + record.Status
	if _, already := o.spokenTasks[spokenKey]; already {
		o.spokenMu.Unlock()
		return
	}
	o.spokenTasks[spokenKey] = struct{}{}
	o.spokenMu.Unlock()

	// Author the announcement with the orchestrator LLM when a provider is
	// available; fall back to a plain template otherwise (headless/tests
	// without a bridge) so a completion is never silently dropped.
	text := o.composeCompletionAnnouncement(sessionID, record)
	if text == "" {
		text = spokenCompletionText(record)
	}
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
	//
	// The event carries TaskID so the widget can (a) dedupe it to exactly one
	// bubble per task id even if the terminal transition is published more than
	// once, and (b) render it as a standalone completion bubble instead of
	// feeding it into the active chat turn's live renderer. The widget treats a
	// task_id-tagged response_delta as ONE whole line, so the full announcement
	// must be published as a single event (not streamed token-by-token).
	if o.bus != nil {
		ev := bridge.NewEvent(bridge.EventResponseDelta)
		ev.SessionID = sessionID
		ev.Delta = text
		ev.TaskID = record.ID
		o.bus.Publish(topicFor(bridge.EventResponseDelta), ev)
	}
}

// composeCompletionAnnouncement asks the orchestrator LLM to announce a
// finished sub-agent's outcome in its own words. It mirrors
// runClarificationResolver: a fresh, correlated actor turn that shares the
// bounded conversation snapshot but runs under its own run id ("announce:<id>")
// so it never collides with the foreground UI orchestrator's mailbox/tool-jobs.
//
// The generation streams into a drained channel (the announcement is not shown
// live token-by-token — the widget expects ONE whole task_id-tagged bubble, see
// speakTaskCompletion); we capture the full assistant text and return it.
//
// Returns "" when no provider is configured so the caller can fall back to the
// plain template.
func (o *Orchestrator) composeCompletionAnnouncement(sessionID string, record taskRecord) string {
	snap := o.snapshot()
	if snap.br == nil {
		return ""
	}

	outcome := "selesai"
	detail := strings.TrimSpace(record.Result)
	switch record.Status {
	case "failed":
		outcome = "gagal"
		detail = strings.TrimSpace(record.Error)
	case "stopped":
		outcome = "dihentikan"
		detail = strings.TrimSpace(record.Result)
	}
	if detail == "" {
		detail = "(tidak ada detail dari sub-agent)"
	}

	user := fmt.Sprintf("Sub-agent `%s` (%s) %s.\n\nTujuan task: %s\n\nLaporan/detail dari sub-agent:\n%s",
		record.ID, record.Role, outcome, strings.TrimSpace(record.Task), detail)

	messages := []bridge.Message{{Role: "user", Content: user}}
	if o.chat != nil {
		if shared, err := o.contextMessages(context.Background(), sessionID, user); err == nil && len(shared) > 0 {
			messages = shared
		}
	}

	// Drain the announcer's own stream; it is captured, not shown live.
	out := make(chan bridge.StreamEvent, 16)
	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()

	all, _ := o.runConversationActor(context.Background(), snap, out, sessionID, "announce:"+record.ID, record.Task, messages, nil)
	close(out)
	<-done

	return strings.TrimSpace(all.String())
}

// spokenCompletionText renders the human-facing chat line for a terminal task.
// It is the FALLBACK used only when no provider is available to author a
// natural announcement (see composeCompletionAnnouncement). Kept short and
// Indonesian-first to match the existing voice.
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
