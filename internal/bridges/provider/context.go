package provider

import (
	"fmt"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/debug"
)

// estimatedCharsPerToken is the rough conversion used by FitMessagesToContext
// when sizing the conversation. 4 characters per token is the high-end of
// English text and stays safely under the model's true window.
const estimatedCharsPerToken = 4

// EstimateTokens returns a rough token count for a single message. The
// estimate is deliberately conservative (over-counts by 1) so we don't
// accidentally exceed the model's true window.
func EstimateTokens(msg bridge.Message) int {
	return (len(msg.Content) + 3) / estimatedCharsPerToken
}

// estimateTotalTokens sums EstimateTokens across all messages.
func estimateTotalTokens(messages []bridge.Message) int {
	total := 0
	for _, m := range messages {
		total += EstimateTokens(m)
	}
	return total
}

// FitMessagesToContext drops the oldest non-system messages until the total
// estimated token count fits inside window. ALL leading system messages (the
// persona prompt, runtime context, negative guidance, prefetch, skills, and -
// after a checkpoint - the checkpoint summary) are always preserved at the
// front, because most APIs reject system messages elsewhere in the transcript
// and the orchestrator depends on those scaffolding blocks being present on
// every turn. When the conversation is already short enough the input slice is
// returned unchanged.
//
// Pass window=0 to no-op (returns messages as-is). Pass window<0 to disable
// truncation entirely.
func FitMessagesToContext(messages []bridge.Message, window int) []bridge.Message {
	fitted, err := FitMessagesToContextStrict(messages, window)
	if err != nil {
		// Compatibility wrapper for callers that only need best-effort sizing.
		// The live bridge uses the strict variant and surfaces the error.
		return messages
	}
	return fitted
}

// FitMessagesToContextStrict preserves the complete current input turn. It
// returns an explicit error when the fixed system prefix or latest non-system
// message cannot fit, rather than silently sending a system-only request.
func FitMessagesToContextStrict(messages []bridge.Message, window int) ([]bridge.Message, error) {
	if window <= 0 {
		return messages, nil
	}
	if estimateTotalTokens(messages) <= window {
		return messages, nil
	}
	// Peel off EVERY leading system message - they must remain contiguous at
	// the front of the output (most APIs reject system messages elsewhere).
	// This is more than the first one: the Ask path stacks persona + runtime +
	// negative guidance + prefetch + skills (+ checkpoint summary) as separate
	// system messages, and a checkpoint rebuild re-issues that whole prefix.
	systemCount := 0
	for systemCount < len(messages) && messages[systemCount].Role == "system" {
		systemCount++
	}
	systemMsgs := messages[:systemCount]
	remaining := messages[systemCount:]
	systemTokens := estimateTotalTokens(systemMsgs)
	budget := window - systemTokens
	if budget <= 0 {
		return nil, fmt.Errorf("provider-bridge: system prompt requires %d tokens, exceeding context window %d", systemTokens, window)
	}
	if len(remaining) > 0 {
		latestTokens := EstimateTokens(remaining[len(remaining)-1])
		if latestTokens > budget {
			return nil, fmt.Errorf("provider-bridge: current input requires %d tokens but only %d remain after system prompts; shorten the message or attachment", latestTokens, budget)
		}
	}
	// Walk from the end, keeping the most recent turns. Stop when adding
	// the next would exceed the remaining budget.
	kept := make([]bridge.Message, 0, len(remaining))
	for i := len(remaining) - 1; i >= 0; i-- {
		candidate := append([]bridge.Message{remaining[i]}, kept...)
		if estimateTotalTokens(candidate) > budget {
			break
		}
		kept = candidate
	}
	dropped := len(remaining) - len(kept)
	debug.Debugf("provider-bridge: context fit window=%d system=%d kept=%d dropped=%d",
		window, systemCount, len(kept), dropped)
	if systemCount > 0 {
		out := make([]bridge.Message, 0, systemCount+len(kept))
		out = append(out, systemMsgs...)
		out = append(out, kept...)
		return out, nil
	}
	return kept, nil
}
