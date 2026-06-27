package orchestrator

import (
	"context"
	"sort"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

type toolBatchOutput struct {
	call    parse.ToolCall
	text    string
	handled bool
}

const maxToolResultUIBytes = 24 * 1024

func truncateToolResultForUI(text string) string {
	if isInlineImageToolResult(text) {
		return "[image payload delivered to the model]"
	}
	if len(text) <= maxToolResultUIBytes {
		return text
	}
	return text[:maxToolResultUIBytes] + "\n\n[output truncated for display]"
}

// executeToolBatch submits independent tool calls together. Terminal lifecycle
// calls are a barrier: all ordinary jobs finish before complete/fail/
// clarification can mutate the actor's terminal state.
func (o *Orchestrator) executeToolBatch(ctx context.Context, runID, sessionID string, tools []scheduledTool) ([]toolBatchOutput, bool) {
	var ordinary, barriers []scheduledTool
	for _, item := range tools {
		if toolIsBarrier(item.call.Name) {
			barriers = append(barriers, item)
		} else {
			ordinary = append(ordinary, item)
		}
	}

	results := make([]toolJobResult, 0, len(tools))
	results = append(results, o.collectToolJobs(ctx, runID, sessionID, ordinary)...)
	if ctx.Err() == nil {
		results = append(results, o.collectToolJobs(ctx, runID, sessionID, barriers)...)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].job.Index < results[j].job.Index })

	outputs := make([]toolBatchOutput, 0, len(results))
	stop := false
	for _, result := range results {
		outputs = append(outputs, toolBatchOutput{
			call: result.job.ToolCall, text: result.outcome.text, handled: result.outcome.handled,
		})
		if result.outcome.handled {
			// Emit only the tool's own output. The internal job ID
			// ("[job job-…]") is meaningless to the model and, when echoed
			// back by a non-native model, leaked raw into the user-facing
			// answer - so it is intentionally dropped here.
		}
		stop = stop || result.outcome.stop
	}
	return outputs, stop
}

func (o *Orchestrator) collectToolJobs(ctx context.Context, runID, sessionID string, tools []scheduledTool) []toolJobResult {
	if len(tools) == 0 {
		return nil
	}
	ch := o.toolJobs().submitBatch(ctx, runID, sessionID, tools)
	results := make([]toolJobResult, 0, len(tools))
	for {
		select {
		case <-ctx.Done():
			o.toolJobs().cancelRun(runID)
			return results
		case result, ok := <-ch:
			if !ok {
				return results
			}
			results = append(results, result)
		}
	}
}

func toolIsBarrier(name string) bool {
	switch name {
	// Terminal lifecycle + compaction are barriers: their result mutates the
	// actor's terminal state, so all ordinary jobs must finish first. `wait`
	// and `sapaloq_cancel_job` are NOT barriers - they collect/control other
	// jobs and must run alongside them.
	case "sapaloq_complete_task", "sapaloq_fail_task", "request_clarification", "sapaloq_stop":
		return true
	default:
		return false
	}
}
