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

// runSubAgentLoop drives a sub-agent (planner or task-runner) as a multi-turn
// tool loop: the model can call assessment/plan/exec tools, receive results,
// and continue until it completes/fails the task or the budget runs out. This
// is what makes Plan and Agent actually able to read, search, web-research,
// write, and run — instead of emitting one blind blob of text.
//
// record is mutated in place (Status/Result/Error/Question); the caller
// persists the final state.
func (o *Orchestrator) runSubAgentLoop(ctx context.Context, snap providerSnapshot, sessionID string, record *taskRecord) {
	tools := o.toolsForRole(record.Role)
	messages := o.buildSubAgentMessages(record)
	subSession := sessionID + ":" + record.ID
	maxTurns := o.roleMaxTurns(record.Role)

	// The persisted transcript + answer have been folded into `messages` by
	// buildSubAgentMessages. Clear the consumed Answer so it isn't replayed
	// again on a subsequent pause; KEEP the existing Transcript so this run
	// appends to it (preserving full context across multiple clarifications).
	record.Answer = ""

	var finalResult strings.Builder
	// idleNudges counts consecutive turns where an executor role (task-runner)
	// produced neither a tool call nor a terminal event. Such a turn is usually
	// the model "announcing intent" (e.g. "I'll switch to system_exec") without
	// acting. Treating that as completion ends the task prematurely (the
	// observed "kepentok" bug), so instead we nudge it to act or finish — but
	// only a bounded number of times so a stuck model can't loop forever.
	idleNudges := 0
	const maxIdleNudges = 2

	for turn := 1; turn <= maxTurns; turn++ {
		if ctx.Err() != nil {
			record.Status = "stopped"
			return
		}
		cleanMessages, images := extractImages(messages)
		stream, err := snap.br.Complete(ctx, bridge.Request{
			SessionID:     subSession,
			Model:         snap.entry.Model,
			Messages:      cleanMessages,
			DeclaredTools: tools,
			Images:        images,
		})
		if err != nil {
			if ctx.Err() != nil {
				record.Status = "stopped"
			} else {
				record.Status = "failed"
				record.Error = err.Error()
			}
			return
		}

		var turnText strings.Builder
		var toolResults []string
		terminal := false
		for ev := range stream {
			if ev.SessionID == "" {
				ev.SessionID = record.ID
			}
			_ = o.progress.Append(record.ID, ev)
			switch ev.Kind {
			case bridge.EventResponseDelta:
				turnText.WriteString(ev.Delta)
				finalResult.WriteString(ev.Delta)
			case bridge.EventError:
				record.Error = ev.Error
			case bridge.EventToolCall:
				if ev.ToolCall == nil {
					continue
				}
				res := o.handleSubAgentTool(ctx, record, &finalResult, *ev.ToolCall)
				if res.text != "" {
					toolResults = append(toolResults, fmt.Sprintf("[%s] %s", ev.ToolCall.Name, res.text))
				}
				terminal = terminal || res.terminal
			}
		}

		if record.Error != "" && len(toolResults) == 0 {
			record.Status = "failed"
			record.Result = strings.TrimSpace(finalResult.String())
			return
		}
		if terminal {
			// complete/fail tool already set Status/Result/Error.
			if record.Result == "" {
				record.Result = strings.TrimSpace(finalResult.String())
			}
			return
		}
		if record.Status == "awaiting_clarification" {
			// Pause the loop and persist the transcript so the task can be
			// resumed (sapaloq_answer_clarification) with its accumulated
			// context rather than re-spawned from scratch.
			record.appendTranscript("assistant", turnText.String())
			if len(toolResults) > 0 {
				record.appendTranscript("user", "[Tool results]\n"+strings.Join(toolResults, "\n\n"))
			}
			record.Result = strings.TrimSpace(finalResult.String())
			_ = o.writeTask(*record)
			return
		}
		if len(toolResults) == 0 {
			// No tools called this turn. For a planner, that plan.md is the
			// authoritative result; planner/scribe finish naturally here.
			if record.Role == "planner" {
				if plan := o.readPlanMarkdown(record.ID); plan != "" {
					record.Result = plan
					record.Status = "done"
					return
				}
			}
			// Executors (task-runner) must signal completion explicitly via
			// sapaloq_complete_task / sapaloq_fail_task. A tool-less turn is
			// almost always the model narrating intent without acting — do NOT
			// silently mark it done. Nudge it to either act or finish, bounded
			// by maxIdleNudges so a stuck model still terminates.
			if record.Role == "task-runner" && idleNudges < maxIdleNudges {
				idleNudges++
				nudge := "You did not call any tool this turn and have not finished. " +
					"If the task is complete, call sapaloq_complete_task with a summary. " +
					"If it cannot be done, call sapaloq_fail_task with a reason. " +
					"Otherwise, actually invoke the tool you need (e.g. system_exec, " +
					"terminal_run, workspace_create_file) — do not just describe what you will do."
				record.appendTranscript("assistant", turnText.String())
				record.appendTranscript("user", nudge)
				messages = append(messages,
					bridge.Message{Role: "assistant", Content: turnText.String()},
					bridge.Message{Role: "user", Content: nudge},
				)
				images = nil
				continue
			}
			record.Result = strings.TrimSpace(finalResult.String())
			record.Status = "done"
			return
		}

		// A productive turn (tools ran) resets the idle nudge counter.
		idleNudges = 0

		// Feed tool results back and continue. Also record the turn in the
		// resumable transcript so a later clarification pause keeps full context.
		toolResultsMsg := "[Tool results]\n" + strings.Join(toolResults, "\n\n")
		record.appendTranscript("assistant", turnText.String())
		record.appendTranscript("user", toolResultsMsg)
		messages = append(messages,
			bridge.Message{Role: "assistant", Content: turnText.String()},
			bridge.Message{Role: "user", Content: toolResultsMsg + "\nContinue the task using these results. When finished, call sapaloq_complete_task (agent) or output the final plan (planner)."},
		)
		images = nil
	}

	// Budget exhausted.
	record.Result = strings.TrimSpace(finalResult.String())
	if record.Status == "in_progress" {
		record.Error = fmt.Sprintf("sub-agent stopped after %d turns without completing", maxTurns)
		record.Status = "failed"
	}
}

// roleMaxTurns resolves the tool-loop budget for a sub-agent role, preferring
// the per-role maxTurns from config.json and falling back to subAgentMaxTurns.
// The value is clamped to a sane range so a bad config can't hang or starve.
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
	if turns > 60 {
		turns = 60
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
	case "workspace_write_file", "workspace_create_file", "workspace_edit_file",
		"workspace_delete_file", "terminal_run":
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
	case "workspace_write_file":
		return subToolResult{text: toolWriteFile(args, false)}
	case "workspace_create_file":
		return subToolResult{text: toolWriteFile(args, true)}
	case "workspace_edit_file":
		return subToolResult{text: toolEditFile(args)}
	case "workspace_delete_file":
		return subToolResult{text: toolDeleteFile(args)}
	case "terminal_run":
		return subToolResult{text: toolTerminalRun(ctx, args)}
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
