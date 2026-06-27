package orchestrator

// subagent.go drives the sub-agent inference loop. It owns the lifecycle
// (spawn → run → tool dispatch → finalise / fail) and the routing of every
// tool the sub-agent can call. The system-prompt and message-assembly helpers
// it relies on (buildSubAgentMessages, readPlanMarkdown) live in prompt.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

// unlimitedTurnsBudget is the sentinel roleMaxTurns returns for a role that
// should run without any turn ceiling (the executor): runTurnLoop treats a
// negative budget as unbounded, leaving the real anomaly guards (no-progress,
// identical-tool, wall-time, tool-call) as the only stoppers.
const unlimitedTurnsBudget = -1

// minSubAgentMaxTurns is only a sane floor for a misconfigured POSITIVE
// per-role value (so a config of a tiny number can't starve a run); it never
// applies to the unlimited sentinel.
const minSubAgentMaxTurns = 1

// runSubAgentLoop drives a sub-agent (planner / task-runner / scribe) on the
// SAME inference engine as chat (runTurnLoop). Planner and Agent are therefore
// just chat with a different system prompt + tool set + output sink: they reuse
// the chat loop's budgets, loop-detection, compaction and clean stream/error
// handling instead of a separate, perennially-buggy copy. This adapter only
// supplies the role-specific pieces:
//   - tools:        o.toolsForRole(record.Role)
//   - dispatch:     handleSubAgentTool (terminal tools mutate record + stop)
//   - sink:         progress JSONL + worker heartbeat (so a live stream never
//     looks stalled to the watchdog - the recurring stall bug)
//   - finish:       every role ends by calling an explicit terminal tool
//     (sapaloq_stop / sapaloq_complete_task / sapaloq_fail_task). A tool-less
//     turn is NOT a stop signal; the shared toolless-turn budget bounds a
//     model that only narrates intent without ever stopping.
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

	compactCtx := &subAgentCompactCtx{
		fallbackTask:    record.Task,
		sink:            &subagentSink{o: o, taskID: record.ID},
		taskID:          record.ID,
		parentSessionID: sessionID,
	}

	cfg := turnConfig{
		sessionID:         subSession,
		runID:             record.ID,
		tools:             o.toolsForRole(record.Role),
		sink:              compactCtx.sink,
		compactCtx:        compactCtx,
		maxInferenceTurns: o.roleMaxTurns(record.Role),
		dispatch: func(ctx context.Context, call parse.ToolCall) turnOutcome {
			o.publishTaskActivity(sessionID, *record, "Menjalankan `"+call.Name+"`.")
			res := o.handleSubAgentTool(ctx, record, &finalResult, call, compactCtx)
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
	// Flush + close the per-task progress drain so the JSONL is fully
	// persisted and the drain goroutine does not outlive the loop. runBackgroundTask
	// also closes this; Close is idempotent and safe to call twice.
	if o.progress != nil {
		o.progress.Close(record.ID)
	}
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
	// No tools, no terminal signal, no error: the loop ended via the
	// toolless-turn budget (or the per-role turn cap) without the model ever
	// calling sapaloq_stop / sapaloq_complete_task / sapaloq_fail_task. An
	// executor that never signalled completion is a failure; a non-executor
	// (planner without a plan) is done.
	// NOTE: we intentionally do NOT fail a planner that finished cleanly with
	// no plan.md - a planner may legitimately answer a question without
	// producing a formal plan (see TestPlannerCompletesOnToolLessTurn). The
	// field "halu sukses" bug was NOT this case: there the planner's LLM call
	// failed with a provider 500, which now returns a non-nil error and is
	// handled by the `if err != nil` branch above (→ failed). A clean finish
	// with no error is a real, non-failing outcome.
	if record.Role == "task-runner" {
		record.Status = "failed"
		record.Error = "executor stopped without calling `sapaloq_complete_task`, `sapaloq_fail_task`, or `sapaloq_stop`"
		return
	}
	record.Status = "done"
}

// roleMaxTurns resolves the tool-loop budget for a sub-agent role.
//
//   - An explicit per-role maxTurns in config.json always wins (honored as-is,
//     no upper clamp; a tiny positive value is floored to minSubAgentMaxTurns).
//   - Otherwise the EXECUTOR (task-runner) runs UNLIMITED: it does the heavy
//     lifting (many read/edit/run/verify steps over a long task - scaffolding a
//     whole app can take hundreds of tool calls), so an arbitrary turn ceiling
//     would force-fail a productive agent with "inference-turn budget
//     exhausted". A genuinely stuck/looping model is still bounded by the real
//     anomaly guards (toolless-turn budget, identical-tool, wall-time,
//     tool-call) - none of which depend on the turn count.
//   - Every other (short-lived) role - planner, scribe - falls back to the same
//     budget the chat loop uses (Continuation.MaxInferenceTurns, default 128).
func (o *Orchestrator) roleMaxTurns(role string) int {
	if roles := o.cfg.SubAgents.Roles; roles != nil {
		if r, ok := roles[role]; ok && r.MaxTurns > 0 {
			turns := r.MaxTurns
			if turns < minSubAgentMaxTurns {
				turns = minSubAgentMaxTurns
			}
			return turns
		}
	}
	if role == "task-runner" {
		return unlimitedTurnsBudget
	}
	return o.cfg.Orchestrator.WithDefaults().Continuation.MaxInferenceTurns
}

// roleAllows reports whether a sub-agent role may invoke a given tool. When the
// role declares an explicit allowedTools list in config, that list is the
// authority (supporting exact names and `*`-suffix wildcards like `desktop_*`).
// When the role is NOT configured (or has an empty allowlist), we fall back to
// the original hard-coded policy: task-runner may use any tool; every other
// role is read-only (mutating tools - write/create/edit/delete/terminal - are
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
// "gnome_*") would otherwise deny every real tool at execution - a silent,
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

type subToolResult struct {
	text     string
	terminal bool
}

// handleSubAgentTool executes a tool call inside a sub-agent loop: shared
// assessment tools plus plan/lifecycle/clarification tools.
func (o *Orchestrator) handleSubAgentTool(ctx context.Context, record *taskRecord, result *strings.Builder, call parse.ToolCall, compactCtx *subAgentCompactCtx) subToolResult {
	o.auditTool(record.SessionID, "subagent:"+record.Role, call)
	// Enforce role policy before dispatching shared tools. Shared means the
	// implementation is reusable across roles, not that every role may invoke
	// every shared tool. This is especially important for undeclared/provider-
	// poisoned calls that were not present in the role's offered tool surface.
	if !o.roleAllows(record.Role, call.Name) {
		return subToolResult{text: fmt.Sprintf("Error: %s is not allowed for role %s.", call.Name, record.Role)}
	}
	// Shared read-only assessment + web + exec tools (honors wait_for_output).
	if text, ok := o.runSharedTool(ctx, call); ok {
		return subToolResult{text: text}
	}
	args := parseToolArgs(call.Arguments)
	args = o.resolveActorArgs(ctx, args)
	// Sub-agent work tools (write/edit/delete/scribe/notify/write_plan) also
	// honor wait_for_output:false — dispatched to the background registry and
	// collected later via `wait`. Lifecycle tools below intentionally ignore
	// the flag (their result IS the transition and cannot be deferred).
	if run, ok := o.subAgentWorkRunner(args, call.Name, record, result); ok {
		if args.WaitForOutput != nil && !*args.WaitForOutput {
			return subToolResult{text: o.spawnBgTool(ctx, call.Name, run)}
		}
		out, _ := run(ctx)
		return subToolResult{text: out}
	}
	switch call.Name {
	case "desktop_dnd_status":
		return subToolResult{text: o.toolDesktopDNDStatus(ctx)}
	case "read_plan":
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
	case "sapaloq_compact_session":
		args := parseToolArgs(call.Arguments)
		text, _ := o.handleSubAgentCompactSession(ctx, compactCtx, args.Summary, args.Reason)
		return subToolResult{text: text}
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
	case "sapaloq_stop":
		// Explicit stop: available to EVERY sub-agent role (planner, agent,
		// scribe) so the model itself ends its run instead of the orchestrator
		// guessing "no tool = done". A clean stop resolves to `done` with the
		// accumulated free-form text (or the reason) as the result; a planner's
		// plan.md remains its authoritative result via resolveOutcome.
		reason := strings.TrimSpace(args.Reason)
		summary := strings.TrimSpace(result.String())
		if summary == "" {
			summary = reason
		}
		if summary == "" {
			summary = "stopped by agent"
		}
		record.Result = summary
		record.Status = "done"
		msg := "Stopped."
		if reason != "" {
			msg = "Stopped: " + reason
		}
		return subToolResult{text: msg, terminal: true}
	case "request_clarification", "sapaloq_request_decision":
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
	case "sapaloq_send_steering":
		target := strings.TrimSpace(args.TargetTaskID)
		message := strings.TrimSpace(args.Message)
		if target == "" || message == "" {
			return subToolResult{text: "Error: target_task_id and message are required."}
		}
		err := o.enqueueActorEvent(actorControlEvent{
			Kind:          "steering.proposed",
			SessionID:     record.SessionID,
			SourceID:      record.ID,
			TargetID:      target,
			CorrelationID: args.CorrelationID,
			Message:       message,
			Priority:      args.Priority,
		})
		if err != nil {
			return subToolResult{text: "Error: " + err.Error()}
		}
		return subToolResult{text: fmt.Sprintf("Steering queued for `%s`.", target)}
	case "wait":
		mode := strings.TrimSpace(args.Mode)
		if mode == "" {
			mode = "time"
		}
		switch mode {
		case "time":
			sleep := time.Duration(args.Seconds) * time.Second
			if args.Seconds <= 0 {
				sleep = 30 * time.Second
			}
			if sleep > 600*time.Second {
				sleep = 600 * time.Second
			}
			select {
			case <-ctx.Done():
				return subToolResult{text: "Wait cancelled."}
			case <-time.After(sleep):
			}
			return subToolResult{text: fmt.Sprintf("Waited %ds.", int(sleep.Seconds()))}
		case "tool":
			jobID := strings.TrimSpace(args.JobID)
			if jobID == "" {
				return subToolResult{text: "Error: job_id is required for wait mode=tool."}
			}
			timeout := time.Duration(args.TimeoutSeconds) * time.Second
			if args.TimeoutSeconds < 0 {
				timeout = 30 * time.Second
			}
			if timeout > 300*time.Second {
				timeout = 300 * time.Second
			}
			reg := o.bgJobs()
			if reg == nil {
				return subToolResult{text: "Error: background job registry unavailable."}
			}
			start := time.Now()
			done, snap := reg.wait(jobID, timeout)
			view := bgJobToView(snap)
			view["waited_ms"] = time.Since(start).Milliseconds()
			if !done {
				view["hint"] = "job is still running. Either call wait again with a larger timeout_seconds, sapaloq_cancel_job(job_id) to abort, or sapaloq_fail_task if it has been too long."
			} else {
				delete(view, "hint")
			}
			raw, err := json.Marshal(view)
			if err != nil {
				return subToolResult{text: fmt.Sprintf("Error: marshal wait result: %v", err)}
			}
			return subToolResult{text: string(raw)}
		case "task":
			taskID := strings.TrimSpace(args.TaskID)
			if taskID == "" {
				taskID = o.latestTaskID()
			}
			record2, changed, err := o.waitForTaskChange(ctx, taskID, args.Seconds, 120)
			if err != nil {
				if ctx.Err() != nil {
					return subToolResult{text: "Wait cancelled."}
				}
				return subToolResult{text: "Wait failed: " + err.Error()}
			}
			if !changed {
				return subToolResult{text: fmt.Sprintf("Task `%s` masih %s setelah jendela tunggu.", record2.ID, record2.Status)}
			}
			resp := fmt.Sprintf("Task `%s` changed to **%s**.", record2.ID, record2.Status)
			if record2.Question != "" {
				resp += "\n\nNeeds clarification: " + record2.Question
			}
			if record2.Result != "" {
				resp += "\n\n" + record2.Result
			}
			if record2.Error != "" {
				resp += "\n\nError: " + record2.Error
			}
			return subToolResult{text: resp}
		case "events":
			timeout := args.TimeoutSeconds
			if timeout <= 0 {
				timeout = 120
			}
			events := o.waitActorEvents(ctx, record.ID, time.Duration(timeout)*time.Second)
			if len(events) == 0 {
				return subToolResult{text: "No actor event arrived before the wait ended."}
			}
			return subToolResult{text: actorEventsPrompt(events)}
		default:
			return subToolResult{text: "Error: unknown wait mode " + mode + " (use time|tool|task|events)."}
		}
	case "sapaloq_cancel_job":
		jobID := strings.TrimSpace(args.JobID)
		if jobID == "" {
			return subToolResult{text: "Error: job_id is required."}
		}
		reg := o.bgJobs()
		if reg == nil {
			return subToolResult{text: "Error: background job registry unavailable."}
		}
		snap, ok := reg.cancel(jobID)
		if !ok {
			return subToolResult{text: fmt.Sprintf("Error: job_id %q not found.", jobID)}
		}
		view := bgJobToView(snap)
		raw, err := json.Marshal(view)
		if err != nil {
			return subToolResult{text: fmt.Sprintf("Error: marshal cancel: %v", err)}
		}
		return subToolResult{text: string(raw)}
	default:
		return subToolResult{text: "Error: unknown tool " + call.Name}
	}
}

// subAgentWorkRunner returns the background-run function for a sub-agent work
// tool (write_file/create_file/edit_file/delete_file/scribe_write_note/
// desktop_notify/write_plan). Each reproduces the inline handler's behavior
// so a fire-and-forget call collects the same result. write_plan mutates the
// planner's result buffer + record (captured by pointer) exactly as the
// inline path does. Returns (nil, false) for non-work names so the caller
// falls through to the lifecycle switch.
func (o *Orchestrator) subAgentWorkRunner(args toolArgs, name string, record *taskRecord, result *strings.Builder) (bgJobRun, bool) {
	switch name {
	case "write_file":
		return func(context.Context) (string, error) { return toolWriteFile(args, false), nil }, true
	case "create_file":
		return func(context.Context) (string, error) { return toolWriteFile(args, true), nil }, true
	case "edit_file":
		return func(context.Context) (string, error) { return toolEditFile(args), nil }, true
	case "delete_file":
		return func(context.Context) (string, error) { return toolDeleteFile(args), nil }, true
	case "scribe_write_note":
		return func(context.Context) (string, error) { return o.toolScribeWriteNote(args), nil }, true
	case "desktop_notify":
		return func(ctx context.Context) (string, error) { return o.toolDesktopNotify(ctx, args), nil }, true
	case "write_plan":
		md := strings.TrimSpace(args.Markdown)
		if md == "" {
			return func(context.Context) (string, error) { return "Error: markdown is required.", nil }, true
		}
		return func(context.Context) (string, error) {
			result.Reset()
			result.WriteString(md)
			record.Result = md
			_ = writeFileAtomic(filepath.Join(o.taskDir(record.ID), "plan.md"), []byte(md+"\n"), 0o600)
			return fmt.Sprintf("Plan saved to state/tasks/%s/plan.md. You may refine it (read it back with read_plan and rewrite) or stop to finalize.", record.ID), nil
		}, true
	default:
		return nil, false
	}
}
