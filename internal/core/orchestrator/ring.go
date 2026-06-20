package orchestrator

import "github.com/jahrulnr/sapaloq/internal/bridge"

type RingState string

const (
	RingIdle       RingState = "idle"
	RingThinking   RingState = "thinking"
	RingDelegating RingState = "delegating"
	RingNeedsInput RingState = "needs-input"
)

func RingStateFor(kind bridge.EventKind) RingState {
	switch kind {
	case bridge.EventThinkingDelta:
		return RingThinking
	case bridge.EventToolCall:
		return RingDelegating
	case bridge.EventStatus:
		return RingThinking
	case bridge.EventError, bridge.EventDone:
		return RingIdle
	default:
		return RingIdle
	}
}
