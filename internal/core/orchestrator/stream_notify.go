package orchestrator

import (
	"context"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// streamNotify forwards provider stream events to the run sink (widget or progress JSONL).
// No store writes — notification only.
func streamNotify(out turnSink, ctx context.Context, ev bridge.StreamEvent) {
	out.emit(ctx, ev)
}

func streamNotifyBeat(out turnSink, phase string) {
	out.beat(phase)
}
