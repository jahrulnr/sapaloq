package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

// ActorRun is the single execution descriptor for foreground Ask, invisible
// mediators, planners, executors and scribes. Roles differ only by prompt,
// tools, persistence identity and terminal policy.
type ActorRun struct {
	ID              string
	ParentSessionID string
	Role            string
	GenerationID    string
	TaskText        string
	PlanTaskID      string
	Answer          string
	Tools           []string
	Messages        []bridge.Message
	Foreground      bool
	Out             chan<- bridge.StreamEvent
	Record          *taskRecord
	ThinkingOut     *strings.Builder
	result          *strings.Builder
	compactCtx      *subAgentCompactCtx
}

func (o *Orchestrator) runActor(ctx context.Context, snap providerSnapshot, actor ActorRun) (strings.Builder, error) {
	messages, err := o.buildActorMessages(ctx, actor)
	if err != nil {
		return strings.Builder{}, err
	}
	if actor.ID == "" {
		actor.ID = actor.ParentSessionID
	}
	if actor.Role == "" {
		actor.Role = "ask"
	}
	if len(actor.Tools) == 0 {
		if actor.Foreground {
			actor.Tools = askTools
		} else {
			actor.Tools = o.toolsForRole(actor.Role)
		}
	}
	if actor.result == nil {
		actor.result = &strings.Builder{}
	}

	var sink turnSink
	if actor.Foreground {
		sink = chatSink{o: o, out: actor.Out, sessionID: actor.ParentSessionID, widget: true}
	} else {
		if actor.compactCtx == nil {
			actor.compactCtx = &subAgentCompactCtx{
				fallbackTask: actor.TaskText, taskID: actor.ID,
				parentSessionID: actor.ParentSessionID,
			}
		}
		if actor.compactCtx.sink == nil {
			actor.compactCtx.sink = &subagentSink{o: o, taskID: actor.ID, parentSessionID: actor.ParentSessionID}
		}
		sink = actor.compactCtx.sink
	}

	eventSessionID, persistID := actor.ID, actor.ID
	if actor.Foreground {
		eventSessionID, persistID = actor.ParentSessionID, actor.ParentSessionID
	}
	cfg := turnConfig{
		sessionID:       eventSessionID,
		persistID:       persistID,
		runID:           actor.ID,
		tools:           actor.Tools,
		sink:            sink,
		thinkingOut:     actor.ThinkingOut,
		recordToolTurns: true,
		foregroundAsk:   actor.Foreground && actor.Role == "ask",
		generationID:    actor.GenerationID,
		compactCtx:      actor.compactCtx,
		taskAnchor:      actor.TaskText,
		dispatch: func(callCtx context.Context, call parse.ToolCall) turnOutcome {
			return o.dispatchTool(callCtx, snap, actor, call)
		},
	}
	if !actor.Foreground {
		cfg.maxInferenceTurns = o.policyForRole(actor.Role).MaxTurns
	}
	return o.runTurnLoop(ctx, snap, actor.TaskText, messages, cfg)
}

// dispatchTool is the only dispatcher called by the inference engine. The
// role-specific helpers below it own product semantics, while parsing/audit,
// policy gating and result normalization converge here.
func (o *Orchestrator) dispatchTool(ctx context.Context, snap providerSnapshot, actor ActorRun, call parse.ToolCall) turnOutcome {
	args := o.resolveActorArgs(ctx, parseToolArgs(call.Arguments))
	if !actorAllowsTool(actor, call.Name) {
		if _, known := knownToolSet()[call.Name]; !known {
			return turnOutcome{}
		}
		return turnOutcome{text: fmt.Sprintf("Error: %s is not allowed for role %s.", call.Name, actor.Role), handled: true}
	}
	if !actor.Foreground && !o.roleAllows(actor.Role, call.Name) {
		return turnOutcome{text: fmt.Sprintf("Error: %s is not allowed for role %s.", call.Name, actor.Role), handled: true}
	}
	sourceID := actor.ID
	if actor.Foreground {
		sourceID = actor.ParentSessionID
	}
	o.auditTool(sourceID, "actor:"+actor.Role, call)
	if text, ok := o.runSharedToolArgs(ctx, call.Name, args); ok {
		return turnOutcome{text: text, handled: true}
	}
	if actor.Foreground {
		return o.dispatchAskTool(ctx, snap, actor.Out, actor.ParentSessionID, actor.TaskText, call, args)
	}
	if actor.Record == nil {
		return turnOutcome{text: "Error: background actor record is missing.", handled: true, stop: true}
	}
	o.publishTaskActivity(actor.ParentSessionID, *actor.Record, "Menjalankan `"+call.Name+"`.")
	res := o.runBackgroundTool(ctx, actor.Record, actor.result, call, args, actor.compactCtx)
	text := ""
	if res.text != "" {
		text = fmt.Sprintf("[%s] %s", call.Name, res.text)
	}
	return turnOutcome{
		text:    text,
		handled: res.text != "",
		stop:    res.stop || actor.Record.Status == "awaiting_clarification",
	}
}

func actorAllowsTool(actor ActorRun, name string) bool {
	for _, allowed := range actor.Tools {
		if allowed == name {
			return true
		}
	}
	return false
}
