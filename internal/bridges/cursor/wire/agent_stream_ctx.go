package wire

import (
	"context"
)

// agentStreamRunCtx returns the stream context and optional idle watch.
func agentStreamRunCtx(parent context.Context, opts AgentStreamOptions) (context.Context, *StreamIdleWatch) {
	runCtx, cancel := context.WithCancel(parent)
	if opts.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, opts.Timeout)
	}
	watch := NewStreamIdleWatch(cancel, opts.IdleTimeout)
	return runCtx, watch
}

func bindAgentStreamIdle(state *agentStreamState, watch *StreamIdleWatch) {
	if state == nil || watch == nil {
		return
	}
	write := state.writeFrame
	state.writeFrame = func(frame []byte) error {
		watch.Reset()
		return write(frame)
	}
	state.onActivity = watch.Reset
	state.pauseIdle = watch.Pause
	state.resumeIdle = watch.Resume
}
