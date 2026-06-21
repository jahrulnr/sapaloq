package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

var defaultAllowedPaths = []string{
	"/orchestrator/continuation",
	"/orchestrator/compaction",
	"/orchestrator/completion",
	"/subAgents",
	"/feedback/explicitSignalsEnabled",
	"/feedback/maxNegativeSlicesPerTurn",
	"/storage",
}

func (o *Orchestrator) handleSettings(ctx context.Context, out chan<- bridge.StreamEvent, sessionID, message string) bool {
	args := strings.TrimSpace(strings.TrimPrefix(message, "/settings"))
	if args == "" {
		return o.emit(ctx, out, settingsHelpEvent(sessionID))
	}
	if strings.HasPrefix(args, "patch ") {
		return o.handleSettingsPatch(ctx, out, sessionID, strings.TrimSpace(args[6:]))
	}
	if strings.HasPrefix(args, "show") {
		return o.emit(ctx, out, settingsShowEvent(sessionID, o.cfg))
	}
	return o.emit(ctx, out, settingsHelpEvent(sessionID))
}

func (o *Orchestrator) handleSettingsPatch(ctx context.Context, out chan<- bridge.StreamEvent, sessionID, jsonBody string) bool {
	var patch map[string]any
	if err := json.Unmarshal([]byte(jsonBody), &patch); err != nil {
		ev := bridge.NewEvent(bridge.EventError)
		ev.SessionID = sessionID
		ev.Error = fmt.Sprintf("invalid patch JSON: %v", err)
		return o.emit(ctx, out, ev)
	}
	raw, err := config.LoadRaw(o.cfgPath)
	if err != nil {
		ev := bridge.NewEvent(bridge.EventError)
		ev.SessionID = sessionID
		ev.Error = err.Error()
		return o.emit(ctx, out, ev)
	}
	allowed := defaultAllowedPaths
	if err := config.ApplyPatch(raw, patch, allowed); err != nil {
		ev := bridge.NewEvent(bridge.EventError)
		ev.SessionID = sessionID
		ev.Error = err.Error()
		return o.emit(ctx, out, ev)
	}
	reloaded, err := config.ValidateRaw(raw)
	if err != nil {
		ev := bridge.NewEvent(bridge.EventError)
		ev.SessionID = sessionID
		ev.Error = "invalid config patch: " + err.Error()
		return o.emit(ctx, out, ev)
	}
	if err := config.SaveRaw(o.cfgPath, raw, "sub-agent:settings"); err != nil {
		ev := bridge.NewEvent(bridge.EventError)
		ev.SessionID = sessionID
		ev.Error = err.Error()
		return o.emit(ctx, out, ev)
	}
	if err := o.applyConfig(reloaded); err != nil {
		ev := bridge.NewEvent(bridge.EventError)
		ev.SessionID = sessionID
		ev.Error = err.Error()
		return o.emit(ctx, out, ev)
	}
	if info, _ := os.Stat(o.cfgPath); info != nil {
		o.mu.Lock()
		o.cfgModTime = info.ModTime()
		o.mu.Unlock()
	}
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = fmt.Sprintf("config.json updated (%d top-level keys patched).", len(patch))
	return o.emit(ctx, out, ev)
}

func settingsHelpEvent(sessionID string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = "Settings: `/settings patch {\"notifications\":{\"read\":false}}` or `/settings show`."
	return ev
}

func settingsShowEvent(sessionID string, cfg config.Config) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	entry, _ := cfg.LLMBridge.ActiveProvider()
	thinking := strings.TrimSpace(entry.ReasoningEffort)
	if thinking == "" {
		thinking = "default"
	}
	ev.Delta = fmt.Sprintf("driver=%s model=%s thinking=%s socket=%s updatedAt=%s",
		entry.Driver,
		entry.Model,
		thinking,
		cfg.Events.Bus.SocketPath,
		time.Now().UTC().Format(time.RFC3339),
	)
	return ev
}
