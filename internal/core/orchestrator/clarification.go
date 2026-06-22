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
// question has been spoken into chat by speakTaskCompletion. This then lets the
// ORCHESTRATOR — reusing the SAME chat engine as a normal turn — try to answer
// the question itself from conversation context:
//
//   - If the chat LLM is confident it calls sapaloq_answer_clarification, which
//     (via handleAskTool) writes the answer and resumes the task in the
//     background. Vendor-to-vendor, with chat as the mediator — exactly the
//     designed loop, and no new bespoke machinery.
//   - If it is not confident (or the auto-answer budget is spent) it simply does
//     not call the tool; the question stays surfaced to the user, who answers
//     with sapaloq_answer_clarification later. No blocking either way.
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

	// Budget: stop auto-answering after a few attempts so a sub-agent that
	// keeps re-asking is escalated to the user instead of looping.
	o.spokenMu.Lock()
	if o.autoClarifyCount == nil {
		o.autoClarifyCount = make(map[string]int)
	}
	if o.autoClarifyCount[record.ID] >= maxAutoClarifyAnswers {
		o.spokenMu.Unlock()
		return // defer to the user (question already spoken)
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

	messages := []bridge.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	// Drain the resolver's own stream output: the user-facing question was
	// already spoken; we don't want the orchestrator's internal deliberation
	// streamed as a second chat bubble. The answer (if any) is delivered as a
	// task resume + the resumed task's eventual spoken completion.
	out := make(chan bridge.StreamEvent, 16)
	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()

	ctx := context.Background()
	_, _ = o.runConversation(ctx, snap, out, sessionID, record.Task, messages, nil)
	close(out)
	<-done
}
