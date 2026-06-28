package codex

import (
	"context"
	"os/exec"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func resolveBinary() (string, error) { return exec.LookPath("codex") }

func send(ctx context.Context, out chan<- bridge.StreamEvent, ev bridge.StreamEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}

func errorEvent(sessionID, message string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventError)
	ev.SessionID = sessionID
	ev.Error = message
	return ev
}
