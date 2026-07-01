package orchestrator

import (
	"context"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// replayContext is the single owner for model-facing replay messages during a
// run: system prefix (not in turns.json) + actorTurnsToMessages(store) + inflight
// rows not yet durable. Inflight holds only the latest continuation fed into the
// next Complete() before the store catches up; clear it after persist + refresh.
type replayContext struct {
	o            *Orchestrator
	persistID    string
	systemPrefix []bridge.Message
	inflight     []bridge.Message
}

func newReplayContext(o *Orchestrator, persistID string, messages []bridge.Message) *replayContext {
	rc := &replayContext{o: o, persistID: persistID}
	for _, m := range messages {
		if m.Role != "system" {
			break
		}
		rc.systemPrefix = append(rc.systemPrefix, m)
	}
	return rc
}

func (rc *replayContext) AppendInflight(msgs ...bridge.Message) {
	rc.inflight = append(rc.inflight, msgs...)
}

func (rc *replayContext) SetInflight(msgs []bridge.Message) {
	rc.inflight = msgs
}

func (rc *replayContext) ClearInflight() {
	rc.inflight = nil
}

func (rc *replayContext) Messages(ctx context.Context) ([]bridge.Message, error) {
	out := make([]bridge.Message, 0, len(rc.systemPrefix)+len(rc.inflight)+8)
	out = append(out, rc.systemPrefix...)
	if rc.o != nil && rc.o.chat != nil && rc.persistID != "" {
		turns, err := rc.o.chat.ActiveTurns(ctx, rc.persistID, false)
		if err != nil {
			return nil, err
		}
		out = append(out, actorTurnsToMessages(turns)...)
	}
	out = append(out, rc.inflight...)
	return out, nil
}

func (rc *replayContext) RefreshInto(ctx context.Context, messages *[]bridge.Message) error {
	if messages == nil {
		return nil
	}
	next, err := rc.Messages(ctx)
	if err != nil {
		return err
	}
	*messages = next
	return nil
}
