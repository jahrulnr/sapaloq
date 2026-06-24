package provider

import (
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
// estimated token count fits inside window. The first system message (if
// any) is always preserved at the front. When the conversation is already
// short enough the input slice is returned unchanged.
//
// Pass window=0 to no-op (returns messages as-is). Pass window<0 to disable
// truncation entirely.
func FitMessagesToContext(messages []bridge.Message, window int) []bridge.Message {
	if window <= 0 {
		return messages
	}
	if estimateTotalTokens(messages) <= window {
		return messages
	}
	// Peel off the leading system message - it must always be the first
	// message in the output (most APIs reject system messages elsewhere).
	var systemMsg bridge.Message
	hasSystem := false
	remaining := messages
	if len(remaining) > 0 && remaining[0].Role == "system" {
		systemMsg = remaining[0]
		hasSystem = true
		remaining = remaining[1:]
	}
	systemTokens := 0
	if hasSystem {
		systemTokens = EstimateTokens(systemMsg)
	}
	budget := window - systemTokens
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
	debug.Debugf("provider-bridge: context fit window=%d kept=%d dropped=%d",
		window, len(kept)+boolToInt(hasSystem), dropped)
	if hasSystem {
		return append([]bridge.Message{systemMsg}, kept...)
	}
	return kept
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
