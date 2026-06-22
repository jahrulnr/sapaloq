package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// subAgentMaxTurns bounds a sub-agent's tool loop so a misbehaving model can't
// loop forever. Generous because real tasks chain several read/edit/run steps.
const subAgentMaxTurns = 24

// runSubAgentLoop drives a sub-agent (planner / task-runner / scribe) on the
// SAME inference engine as chat (runTurnLoop). Planner and Agent are therefore
// just chat with a different system prompt + tool set + output sink: they reuse
// the chat loop's budgets, loop-detection, compaction and clean stream/error
// handling instead of a separate, perennially-buggy copy. This adapter only
// supplies the role-specific pieces:
//   - tools:        o.toolsForRole(record.Role)
//   - dispatch:     handleSubAgentTool (terminal tools mutate record + stop)
//   - sink:         progress JSONL + worker heartbeat (so a live stream never
//                   looks stalled to the watchdog — the recurring stall bug)
//   - finish:       planner/scribe finish on a tool-less turn; an executor must
//                   call a terminal tool (the shared no-progress guard bounds a
//                   model that only narrates intent)
//
// record is mutated in place (Status/Result/Error/Question); the caller
// persists the final state.
func (o *Orchestrator) runSubAgentLoop(ctx context.Context, snap providerSnapshot, sessionID string, record *taskRecord) {
	messages := o.buildSubAgentMessages(record)
	subSession := sessionID + ":" + record.ID

	// The persisted transcript + answer have been folded into `messages` by
	// buildSubAgentMessages. Clear the consumed Answer so it isn't replayed
	// again on a subsequent pause; KEEP the existing Transcript so this run
	// appends to it (preserving full context across multiple clarifications).
	record.Answer = ""

	// finalResult accumulates the model's free-form text so a terminal tool
	// with no explicit summary (and a planner's natural finish) still has a
	// result to fall back on.
	var finalResult strings.Builder

	cfg := turnConfig{
		sessionID:         subSession,
		tools:             o.toolsForRole(record.Role),
		sink:              &subagentSink{o: o, taskID: record.ID},
		finishOnNoTool:    record.Role != "task-runner",
		maxInferenceTurns: o.roleMaxTurns(record.Role),
		dispatch: func(ctx context.Context, call parse.ToolCall) turnOutcome {
			o.publishTaskActivity(sessionID, *record, "Menjalankan `"+call.Name+"`.")
			res := o.handleSubAgentTool(ctx, record, &finalResult, call)
			text := ""
			if res.text != "" {
				text = fmt.Sprintf("[%s] %s", call.Name, res.text)
			}
			// A terminal tool (complete/fail) ends the run. A clarification
			// request is non-terminal in the dispatcher but must also stop the
			// loop so the task can pause and be resumed with the user's answer.
			stop := res.terminal || record.Status == "awaiting_clarification"
			return turnOutcome{text: text, handled: res.text != "", stop: stop}
		},
	}

	all, err := o.runTurnLoop(ctx, snap, record.Task, messages, cfg)
	finalText := strings.TrimSpace(all.String())
	if finalText == "" {
		finalText = strings.TrimSpace(finalResult.String())
	}

	// Cancellation: the watchdog or a user stop cancelled the context.
	if ctx.Err() != nil {
		if record.Status != "failed" {
			record.Status = "stopped"
		}
		return
	}

	// A terminal tool (sapaloq_complete_task / sapaloq_fail_task) already set
	// Status + Result/Error; just backfill an empty result from the text.
	if record.Status == "done" || record.Status == "failed" {
		if record.Result == "" && record.Error == "" {
			record.Result = finalText
		}
		return
	}

	// Clarification pause: persist the transcript so sapaloq_answer_clarification
	// can resume with full context.
	if record.Status == "awaiting_clarification" {
		record.appendTranscript("assistant", finalText)
		record.Result = finalText
		_ = o.writeTask(*record)
		return
	}

	// The shared loop returned without a terminal tool. Resolve the outcome by
	// role: a planner's plan.md is its authoritative result; otherwise an error
	// from the loop (budget/loop-guard/stream) fails the task with that reason.
	record.Result = finalText
	if record.Role == "planner" {
		if plan := o.readPlanMarkdown(record.ID); plan != "" {
			record.Result = plan
			record.Status = "done"
			return
		}
	}
	if err != nil {
		record.Status = "failed"
		if record.Error == "" {
			record.Error = err.Error()
		}
		o.workerLogError(record.ID, "sub-agent loop ended: "+record.Error)
		return
	}
	// No tools, no terminal signal, no error: an executor that never signalled
	// completion is a failure; a non-executor (planner without a plan) is done.
	if record.Role == "task-runner" {
		record.Status = "failed"
		record.Error = "executor stopped without calling sapaloq_complete_task or sapaloq_fail_task"
		return
	}
	record.Status = "done"
}

// roleMaxTurns resolves the tool-loop budget for a sub-agent role, preferring
// the per-role maxTurns from config.json and falling back to subAgentMaxTurns.
// Only a sane floor is enforced (a config of 0/negative must not starve the
// run to nothing); there is intentionally NO upper clamp, so an operator can
// give a role as much room as they want — the wall-time budget is the single
// final safety net, not an arbitrary turn ceiling.
func (o *Orchestrator) roleMaxTurns(role string) int {
	turns := subAgentMaxTurns
	if roles := o.cfg.SubAgents.Roles; roles != nil {
		if r, ok := roles[role]; ok && r.MaxTurns > 0 {
			turns = r.MaxTurns
		}
	}
	if turns < 1 {
		turns = 1
	}
	return turns
}

// roleAllows reports whether a sub-agent role may invoke a given tool. When the
// role declares an explicit allowedTools list in config, that list is the
// authority (supporting exact names and `*`-suffix wildcards like `desktop_*`).
// When the role is NOT configured (or has an empty allowlist), we fall back to
// the original hard-coded policy: task-runner may use any tool; every other
// role is read-only (mutating tools — write/create/edit/delete/terminal — are
// denied). This preserves backward-compatible, default-deny-for-mutation
// behavior while letting config grant capabilities to named roles.
func (o *Orchestrator) roleAllows(role, tool string) bool {
	if roles := o.cfg.SubAgents.Roles; roles != nil {
		if r, ok := roles[role]; ok && len(r.AllowedTools) > 0 && allowlistMatchesKnownTool(r.AllowedTools) {
			return matchToolAllowlist(r.AllowedTools, tool)
		}
	}
	// Fallback policy (unconfigured role, OR a config allowlist that names only
	// unknown/abstract tools so it would otherwise gate EVERY real tool off and
	// silently brick the sub-agent): task-runner full, others read-only.
	if role == "task-runner" {
		return true
	}
	return !isMutatingTool(tool)
}

// allowlistMatchesKnownTool reports whether a configured allowlist matches at
// least one tool the orchestrator actually implements. A list that names only
// abstract/aspirational tools (e.g. the doc names "exec", "write_file",
// "gnome_*") would otherwise deny every real tool at execution — a silent,
// hard-to-debug failure. When nothing matches we ignore the (clearly wrong)
// list and fall back to the static per-role policy instead.
func allowlistMatchesKnownTool(allow []string) bool {
	for name := range knownToolSet() {
		if matchToolAllowlist(allow, name) {
			return true
		}
	}
	return false
}

// matchToolAllowlist matches a tool name against an allowlist supporting exact
// entries and `*`-suffix wildcards (e.g. "desktop_*").
func matchToolAllowlist(allow []string, tool string) bool {
	for _, a := range allow {
		if a == tool || a == "*" {
			return true
		}
		if strings.HasSuffix(a, "*") && strings.HasPrefix(tool, strings.TrimSuffix(a, "*")) {
			return true
		}
	}
	return false
}

// isMutatingTool flags tools that modify the filesystem or run commands. These
// are denied to read-only roles under the fallback policy.
func isMutatingTool(tool string) bool {
	switch tool {
	case "write_file", "create_file", "edit_file", "delete_file":
		return true
	}
	return false
}

// buildSubAgentMessages assembles the system + user context for a sub-agent,
// including the user's original intent and (for agents) the handed-off plan
// with its acceptance criteria.
func (o *Orchestrator) buildSubAgentMessages(record *taskRecord) []bridge.Message {
	// Role system prompts are file-driven and replaceable (internal/prompts):
	// the on-disk copy is preferred, falling back to the embedded default. An
	// unknown role gets a minimal generic prompt.
	systemContent := o.systemPrompt(record.Role)
	if strings.TrimSpace(systemContent) == "" {
		systemContent = "You are a background SapaLOQ task agent. Use your tools, then return a concise final result."
	}

	messages := []bridge.Message{{Role: "system", Content: systemContent}}

	// Hand off the plan (goal + acceptance criteria) to the agent.
	if record.Role == "task-runner" && record.PlanTaskID != "" {
		if plan := o.readPlanMarkdown(record.PlanTaskID); plan != "" {
			messages = append(messages, bridge.Message{
				Role:    "system",
				Content: "Approved plan to execute (read it as authoritative; satisfy every item under ## Acceptance):\n\n" + plan,
			})
		}
	}

	messages = append(messages, bridge.Message{Role: "user", Content: record.Task})

	// Resume path: if the task has a persisted transcript (it was paused on a
	// clarification), replay it so the sub-agent continues with its prior
	// context. When an Answer is present, append it as the resume nudge.
	if len(record.Transcript) > 0 {
		for _, turn := range record.Transcript {
			role := turn.Role
			if role != "assistant" && role != "user" && role != "system" {
				role = "user"
			}
			messages = append(messages, bridge.Message{Role: role, Content: turn.Content})
		}
	}
	if strings.TrimSpace(record.Answer) != "" {
		messages = append(messages, bridge.Message{
			Role:    "user",
			Content: "Answer to your clarification question: " + strings.TrimSpace(record.Answer) + "\nContinue the task using this answer.",
		})
	}
	return messages
}

func (o *Orchestrator) readPlanMarkdown(planTaskID string) string {
	if planTaskID == "" || filepath.Base(planTaskID) != planTaskID {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(o.taskDir(planTaskID), "plan.md"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

type subToolResult struct {
	text     string
	terminal bool
}

// handleSubAgentTool executes a tool call inside a sub-agent loop: shared
// assessment tools plus plan/lifecycle/clarification tools.
func (o *Orchestrator) handleSubAgentTool(ctx context.Context, record *taskRecord, result *strings.Builder, call parse.ToolCall) subToolResult {
	o.auditTool(record.SessionID, "subagent:"+record.Role, call)
	// Enforce role policy before dispatching shared tools. Shared means the
	// implementation is reusable across roles, not that every role may invoke
	// every shared tool. This is especially important for undeclared/provider-
	// poisoned calls that were not present in the role's offered tool surface.
	if !o.roleAllows(record.Role, call.Name) {
		return subToolResult{text: fmt.Sprintf("Error: %s is not allowed for role %s.", call.Name, record.Role)}
	}
	// Shared read-only assessment + web tools.
	if text, ok := runSharedTool(ctx, call); ok {
		return subToolResult{text: text}
	}
	args := parseToolArgs(call.Arguments)
	switch call.Name {
	case "write_file":
		return subToolResult{text: toolWriteFile(args, false)}
	case "create_file":
		return subToolResult{text: toolWriteFile(args, true)}
	case "edit_file":
		return subToolResult{text: toolEditFile(args)}
	case "delete_file":
		return subToolResult{text: toolDeleteFile(args)}
	case "exec":
		return subToolResult{text: toolExec(ctx, args)}
	case "scribe_write_note":
		return subToolResult{text: o.toolScribeWriteNote(args)}
	case "desktop_notify":
		return subToolResult{text: o.toolDesktopNotify(ctx, args)}
	case "desktop_dnd_status":
		return subToolResult{text: o.toolDesktopDNDStatus(ctx)}
	case "sapaloq_write_plan_markdown":
		md := strings.TrimSpace(args.Markdown)
		if md == "" {
			return subToolResult{text: "Error: markdown is required."}
		}
		result.Reset()
		result.WriteString(md)
		record.Result = md
		_ = writeFileAtomic(filepath.Join(o.taskDir(record.ID), "plan.md"), []byte(md+"\n"), 0o600)
		// Non-terminal: the planner may revise the plan (read it back, rewrite)
		// before finishing. The loop ends naturally when the planner stops
		// calling tools. The path is surfaced so the model knows where it lives.
		return subToolResult{text: fmt.Sprintf("Plan saved to memory/tasks/%s/plan.md. You may refine it (read it back with sapaloq_read_plan_markdown and rewrite) or stop to finalize.", record.ID)}
	case "sapaloq_read_plan_markdown":
		// Planner reads its OWN plan (to iterate); agent reads the handed-off
		// plan it must execute.
		planID := record.PlanTaskID
		if record.Role == "planner" {
			planID = record.ID
		}
		plan := o.readPlanMarkdown(planID)
		if plan == "" {
			return subToolResult{text: "No plan available yet."}
		}
		return subToolResult{text: plan}
	case "sapaloq_update_task_progress":
		note := strings.TrimSpace(args.Note)
		if note == "" {
			return subToolResult{text: "Error: note is required."}
		}
		record.UpdatedAt = time.Now().UTC()
		_ = o.writeTask(*record)
		return subToolResult{text: "Progress noted."}
	case "sapaloq_complete_task":
		summary := strings.TrimSpace(args.Summary)
		if summary == "" {
			summary = strings.TrimSpace(result.String())
		}
		record.Result = summary
		record.Status = "done"
		return subToolResult{text: "Task marked complete.", terminal: true}
	case "sapaloq_fail_task":
		reason := strings.TrimSpace(args.Reason)
		if reason == "" {
			reason = "unspecified failure"
		}
		record.Error = reason
		record.Status = "failed"
		return subToolResult{text: "Task marked failed.", terminal: true}
	case "sapaloq_request_clarification":
		question := strings.TrimSpace(args.Question)
		if question == "" {
			return subToolResult{text: "Error: question is required."}
		}
		if len(args.Options) > 0 {
			question += "\nOptions: " + strings.Join(args.Options, " | ")
		}
		record.Question = question
		record.Status = "awaiting_clarification"
		record.UpdatedAt = time.Now().UTC()
		_ = o.writeTask(*record)
		return subToolResult{text: "Clarification requested from the user.", terminal: false}
	default:
		return subToolResult{text: "Error: unknown tool " + call.Name}
	}
}
