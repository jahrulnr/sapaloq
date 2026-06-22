package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// maxAutoClarifyAnswers bounds how many times the orchestrator may auto-answer a
// single task's clarification questions before it must defer to the user. This
// prevents an auto-answer ↔ re-ask ping-pong between the orchestrator and a
// confused sub-agent from looping forever.
const maxAutoClarifyAnswers = 2

// resolveClarification closes the clarification half of the event-driven design
// (ORCHESTRATOR.md: "sub-agent tanya orchestrator; orchestrator jawab sendiri
// atau forward ke user"). When a sub-agent pauses on awaiting_clarification the
// worker goroutine has already exited cleanly (no blocking, no stall) and the
// question is kept off the UI while this mediator runs. It lets an independent
// decision actor — reusing the SAME inference engine as a normal turn — answer
// the question itself from conversation context:
//
//   - If the chat LLM is confident it calls sapaloq_answer_clarification, which
//     (via handleAskTool) writes the answer and resumes the task in the
//     background. Vendor-to-vendor, with chat as the mediator — exactly the
//     designed loop, and no new bespoke machinery.
//   - If it is not confident (or the auto-answer budget is spent), the decision
//     is explicitly escalated to the UI orchestrator, which is the sole writer
//     of user-visible conversation.
//
// It runs in its own goroutine (the caller is on the worker's terminal path)
// and is idempotent per (task, attempt) via autoClarifyCount.
func (o *Orchestrator) resolveClarification(sessionID string, record taskRecord) {
	if o == nil || sessionID == "" {
		return
	}
	if record.Status != "awaiting_clarification" || strings.TrimSpace(record.Question) == "" {
		return
	}
	_ = o.enqueueActorEvent(actorControlEvent{
		Kind:          "decision.requested",
		SessionID:     sessionID,
		SourceID:      record.ID,
		TargetID:      "decision:" + record.ID,
		CorrelationID: record.ID,
		Message:       record.Question,
	})

	// Budget: stop auto-answering after a few attempts so a sub-agent that
	// keeps re-asking is escalated to the user instead of looping.
	o.spokenMu.Lock()
	if o.autoClarifyCount == nil {
		o.autoClarifyCount = make(map[string]int)
	}
	if o.autoClarifyCount[record.ID] >= maxAutoClarifyAnswers {
		o.spokenMu.Unlock()
		o.escalateClarification(sessionID, record)
		return
	}
	o.autoClarifyCount[record.ID]++
	o.spokenMu.Unlock()

	go o.runClarificationResolver(sessionID, record)
}

// runClarificationResolver performs the actual orchestrator-side reasoning. It
// is split out so resolveClarification stays cheap on the hot terminal path.
func (o *Orchestrator) runClarificationResolver(sessionID string, record taskRecord) {
	snap := o.snapshot()
	if snap.br == nil {
		o.escalateClarification(sessionID, record)
		return
	}

	system := "You are the orchestrator mediating between the user and a background sub-agent. " +
		"A sub-agent is paused and needs a decision to continue. " +
		"If — and ONLY if — you can answer confidently from the conversation context and the user's evident intent, " +
		"call sapaloq_answer_clarification with task_id=\"" + record.ID + "\" and a clear answer. " +
		"If you are not confident, or the decision is genuinely the user's to make, do NOT call any tool and do NOT guess — " +
		"the user has already been shown the question and will answer."

	user := fmt.Sprintf("Background task `%s` (%s) asks:\n\n%s\n\nTask goal was: %s",
		record.ID, record.Role, strings.TrimSpace(record.Question), strings.TrimSpace(record.Task))

	messages := []bridge.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}
	if o.chat != nil {
		if shared, err := o.contextMessages(context.Background(), sessionID, user); err == nil && len(shared) > 0 {
			// Keep a bounded snapshot of the UI conversation and task context,
			// but replace the Ask role prompt with the mediator's narrower
			// authority. This shares knowledge without sharing mutable actor
			// state or the foreground run id.
			shared[0] = bridge.Message{Role: "system", Content: system}
			messages = shared
		}
	}

	// Drain the resolver's own stream output. This mediator is intentionally
	// invisible; only a decision.escalated event is allowed to reach the UI.
	out := make(chan bridge.StreamEvent, 16)
	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()

	ctx := context.Background()
	_, _ = o.runConversationActor(ctx, snap, out, sessionID, "decision:"+record.ID, record.Task, messages, nil)
	close(out)
	<-done

	current, err := o.readTask(record.ID)
	if err != nil || current.Status == "awaiting_clarification" {
		o.escalateClarification(sessionID, record)
		return
	}
	if o.bus != nil {
		ev := bridge.NewEvent(bridge.EventDecisionUpdate)
		ev.SessionID = sessionID
		ev.RunID = record.ID
		ev.TargetID = record.ID
		ev.CorrelationID = record.ID
		ev.Status = "decision.resolved"
		ev.Summary = "Decision mediator answered from shared context; task resumed."
		o.bus.Publish("sapaloq.v1.actor.decision.resolved", ev)
	}
}

func (o *Orchestrator) escalateClarification(sessionID string, record taskRecord) {
	if o.bus != nil {
		ev := bridge.NewEvent(bridge.EventDecisionUpdate)
		ev.SessionID = sessionID
		ev.RunID = record.ID
		ev.TargetID = sessionID
		ev.CorrelationID = record.ID
		ev.Status = "decision.escalated"
		ev.Summary = record.Question
		o.bus.Publish("sapaloq.v1.actor.decision.escalated", ev)
	}
	o.publishTaskUpdateDirect(sessionID, record)
}
