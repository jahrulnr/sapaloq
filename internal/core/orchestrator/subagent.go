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
	tools := toolsForRole(record.Role)
	messages := o.buildSubAgentMessages(record)
	subSession := sessionID + ":" + record.ID
	maxTurns := o.roleMaxTurns(record.Role)

	var finalResult strings.Builder

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
			// Pause the loop; orchestrator/user will resume by clearing the
			// question and re-spawning or answering (MVP: stop here, surface Q).
			record.Result = strings.TrimSpace(finalResult.String())
			return
		}
		if len(toolResults) == 0 {
			// No tools called this turn → the planner/agent is finished.
			// For a planner that wrote a plan.md, that plan is the authoritative
			// result (not any trailing chatter); otherwise use the text.
			if record.Role == "planner" {
				if plan := o.readPlanMarkdown(record.ID); plan != "" {
					record.Result = plan
					record.Status = "done"
					return
				}
			}
			record.Result = strings.TrimSpace(finalResult.String())
			record.Status = "done"
			return
		}

		// Feed tool results back and continue.
		messages = append(messages,
			bridge.Message{Role: "assistant", Content: turnText.String()},
			bridge.Message{Role: "user", Content: "[Tool results]\n" + strings.Join(toolResults, "\n\n") + "\nContinue the task using these results. When finished, call sapaloq_complete_task (agent) or output the final plan (planner)."},
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

// buildSubAgentMessages assembles the system + user context for a sub-agent,
// including the user's original intent and (for agents) the handed-off plan
// with its acceptance criteria.
func (o *Orchestrator) buildSubAgentMessages(record *taskRecord) []bridge.Message {
	var system strings.Builder
	switch record.Role {
	case "planner":
		system.WriteString("You are SapaLOQ's planner (Plan mode). ")
		system.WriteString("Investigate thoroughly with your assessment tools — workspace_read_file (supports offset/limit line ranges), workspace_search, workspace_list_dir, workspace_glob, web_search, web_fetch — then produce a concrete Markdown plan. ")
		system.WriteString("Call sapaloq_write_plan_markdown with sections: ## Goal, ## Constraints, ## Steps (checkbox list), ## Risks, ## Acceptance (checkbox list of verifiable criteria). ")
		system.WriteString("You MAY iterate: after writing, read it back with sapaloq_read_plan_markdown and rewrite to refine it. Stop calling tools when the plan is final. ")
		system.WriteString("By policy (not platform) you stay read-only: do NOT write/edit/delete project files, run mutating commands, or claim implementation — that is the executor's job. If the request is ambiguous, call sapaloq_request_clarification.")
	case "task-runner":
		system.WriteString("You are SapaLOQ's executor (Agent mode) with full tool access. ")
		system.WriteString("Assess first (workspace_read_file with offset/limit, workspace_search, workspace_list_dir, workspace_glob, web_search/fetch), then implement using workspace_edit_file (precise in-place edits), workspace_write_file/workspace_create_file (whole files), workspace_delete_file, and terminal_run. Prefer workspace_edit_file over rewriting whole files. ")
		system.WriteString("Report progress with sapaloq_update_task_progress. ")
		system.WriteString("When the work meets every acceptance criterion, call sapaloq_complete_task with a summary. If you cannot finish, call sapaloq_fail_task with the reason. ")
		system.WriteString("If a decision is genuinely ambiguous, call sapaloq_request_clarification instead of guessing.")
	default:
		system.WriteString("You are a background SapaLOQ task agent. Use your tools, then return a concise final result.")
	}

	messages := []bridge.Message{{Role: "system", Content: system.String()}}

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
	// Shared read-only assessment + web tools.
	if text, ok := runSharedTool(ctx, call); ok {
		return subToolResult{text: text}
	}
	args := parseToolArgs(call.Arguments)
	switch call.Name {
	case "workspace_write_file":
		if record.Role != "task-runner" {
			return subToolResult{text: "Error: write is not allowed in this mode."}
		}
		return subToolResult{text: toolWriteFile(args, false)}
	case "workspace_create_file":
		if record.Role != "task-runner" {
			return subToolResult{text: "Error: create is not allowed in this mode."}
		}
		return subToolResult{text: toolWriteFile(args, true)}
	case "workspace_edit_file":
		if record.Role != "task-runner" {
			return subToolResult{text: "Error: edit is not allowed in this mode."}
		}
		return subToolResult{text: toolEditFile(args)}
	case "workspace_delete_file":
		if record.Role != "task-runner" {
			return subToolResult{text: "Error: delete is not allowed in this mode."}
		}
		return subToolResult{text: toolDeleteFile(args)}
	case "terminal_run":
		if record.Role != "task-runner" {
			return subToolResult{text: "Error: terminal is not allowed in this mode."}
		}
		return subToolResult{text: toolTerminalRun(ctx, args)}
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
