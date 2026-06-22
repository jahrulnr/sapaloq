package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

type actorControlEvent struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	SessionID     string    `json:"session_id"`
	SourceID      string    `json:"source_id,omitempty"`
	TargetID      string    `json:"target_id"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	Message       string    `json:"message"`
	Priority      string    `json:"priority,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func (o *Orchestrator) actorInboxRoot(targetID string) string {
	root := o.stateDir
	if root == "" {
		root = o.memoryDir
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, "actor-inbox", safeActorID(targetID))
}

func safeActorID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, "/", "_")
	id = strings.ReplaceAll(id, string(filepath.Separator), "_")
	if id == "" {
		return "unknown"
	}
	return id
}

func (o *Orchestrator) enqueueActorEvent(ev actorControlEvent) error {
	if ev.ID == "" {
		ev.ID = fmt.Sprintf("event-%d", time.Now().UTC().UnixNano())
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	root := o.actorInboxRoot(ev.TargetID)
	if root != "" {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(ev, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(root, ev.ID+".json"), raw, 0o600); err != nil {
			return err
		}
	}
	o.signalActor(ev.TargetID)
	if o.bus != nil {
		out := bridge.NewEvent(bridge.EventSteeringUpdate)
		if ev.Kind == "decision.requested" || ev.Kind == "decision.resolved" || ev.Kind == "decision.escalated" {
			out.Kind = bridge.EventDecisionUpdate
		}
		out.EventID = ev.ID
		out.CorrelationID = ev.CorrelationID
		out.SessionID = ev.SessionID
		out.RunID = ev.SourceID
		out.TargetID = ev.TargetID
		out.Status = ev.Kind
		out.Summary = ev.Message
		o.bus.Publish("sapaloq.v1.actor."+ev.Kind, out)
	}
	return nil
}

func (o *Orchestrator) signalActor(targetID string) {
	o.controlMu.Lock()
	defer o.controlMu.Unlock()
	if o.controlSignals == nil {
		o.controlSignals = make(map[string]chan struct{})
	}
	if ch := o.controlSignals[targetID]; ch != nil {
		close(ch)
	}
	o.controlSignals[targetID] = make(chan struct{})
}

func (o *Orchestrator) actorSignal(targetID string) <-chan struct{} {
	o.controlMu.Lock()
	defer o.controlMu.Unlock()
	if o.controlSignals == nil {
		o.controlSignals = make(map[string]chan struct{})
	}
	if o.controlSignals[targetID] == nil {
		o.controlSignals[targetID] = make(chan struct{})
	}
	return o.controlSignals[targetID]
}

func (o *Orchestrator) drainActorEvents(targetID string) []actorControlEvent {
	root := o.actorInboxRoot(targetID)
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	out := make([]actorControlEvent, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var ev actorControlEvent
		if json.Unmarshal(raw, &ev) == nil {
			out = append(out, ev)
			_ = os.Remove(path)
		}
	}
	return out
}

func (o *Orchestrator) waitActorEvents(ctx context.Context, targetID string, timeout time.Duration) []actorControlEvent {
	if events := o.drainActorEvents(targetID); len(events) > 0 {
		return events
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	signal := o.actorSignal(targetID)
	select {
	case <-ctx.Done():
		return nil
	case <-timer.C:
		return nil
	case <-signal:
		return o.drainActorEvents(targetID)
	}
}

func actorEventsPrompt(events []actorControlEvent) string {
	if len(events) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Actor events received at a safe point]\n")
	for _, ev := range events {
		source := ev.SourceID
		if source == "" {
			source = "session supervisor"
		}
		fmt.Fprintf(&b, "- %s from %s: %s\n", ev.Kind, source, ev.Message)
	}
	b.WriteString("Apply relevant steering before continuing. If it conflicts with completed work, explain the conflict through sapaloq_send_steering.")
	return strings.TrimSpace(b.String())
}
