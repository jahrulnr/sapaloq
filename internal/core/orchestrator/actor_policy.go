package orchestrator

import "strings"

// rolePolicy captures the only execution-policy differences between actor
// roles. Prompt and tool surfaces remain data owned by their existing config.
type rolePolicy struct {
	MaxTurns        int
	RequireTerminal bool
	PlanIsOutcome   bool
}

func (o *Orchestrator) policyForRole(role string) rolePolicy {
	return rolePolicy{
		MaxTurns:        o.roleMaxTurns(role),
		RequireTerminal: role == "task-runner",
		PlanIsOutcome:   role == "planner",
	}
}

// resolveActorOutcome applies terminal semantics after the shared inference
// loop. It deliberately contains no transport or persistence behavior.
func (o *Orchestrator) resolveActorOutcome(record *taskRecord, finalText string, runErr error) {
	if record.Status == "done" || record.Status == "failed" {
		if record.Result == "" && record.Error == "" {
			record.Result = finalText
		}
		return
	}
	if record.Status == "awaiting_clarification" {
		record.Result = finalText
		return
	}

	policy := o.policyForRole(record.Role)
	record.Result = strings.TrimSpace(finalText)
	if policy.PlanIsOutcome {
		if plan := o.readPlanMarkdown(record.ID); plan != "" {
			record.Result = plan
			record.Status = "done"
			return
		}
	}
	if runErr != nil {
		record.Status = "failed"
		if record.Error == "" {
			record.Error = runErr.Error()
		}
		return
	}
	if policy.RequireTerminal {
		record.Status = "failed"
		record.Error = "executor stopped without calling `sapaloq_complete_task`, `sapaloq_fail_task`, or `sapaloq_stop`"
		return
	}
	record.Status = "done"
}
