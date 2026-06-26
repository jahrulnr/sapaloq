package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

var bridgeFactory func(config.Config) (bridge.Bridge, error)

type providerSnapshot struct {
	cfg   config.Config
	entry config.LLMBridge
	br    bridge.Bridge
}

func SetBridgeFactory(factory func(config.Config) (bridge.Bridge, error)) {
	bridgeFactory = factory
}

func (o *Orchestrator) snapshot() providerSnapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return providerSnapshot{cfg: o.cfg, entry: o.entry, br: o.bridge}
}

func (o *Orchestrator) applyConfig(next config.Config) error {
	entry, err := next.LLMBridge.ActiveProvider()
	if err != nil {
		return err
	}
	snap := o.snapshot()
	br := snap.br
	if bridgeFactory != nil && providerChanged(entry, snap.entry) {
		br, err = bridgeFactory(next)
		if err != nil {
			return err
		}
	}
	dirs := config.RuntimeDirs(next)
	var nextChat *chatstore.Store
	if dirs.MemoryDir != o.memoryDir {
		if err := config.EnsureRuntimeDirs(dirs); err != nil {
			return err
		}
		nextChat, err = chatstore.Open(dirs.MemoryDir)
		if err != nil {
			return fmt.Errorf("chat store: %w", err)
		}
	}
	o.mu.Lock()
	oldChat := o.chat
	o.cfg = next
	o.entry = entry
	o.bridge = br
	o.progress = newAsyncProgressWriter(ProgressWriter{Dir: dirs.ProgressDir})
	// Repoint runtime-state dirs unconditionally: they track DataDir and must
	// stay consistent even when the memory DB itself did not change. Previously
	// workersDir/workers were left dangling on reload - a latent bug.
	o.stateDir = dirs.StateDir
	o.tasksDir = dirs.TasksDir
	o.workspaceDir = dirs.WorkspaceDir
	if dirs.WorkersDir != o.workersDir {
		o.workersDir = dirs.WorkersDir
		o.workers = newWorkerRegistry(dirs.WorkersDir)
	}
	if nextChat != nil {
		o.chat = nextChat
		o.memoryDir = dirs.MemoryDir
	}
	o.mu.Unlock()
	if nextChat != nil && oldChat != nil {
		_ = oldChat.Close()
	}
	return nil
}

func providerChanged(next, current config.LLMBridge) bool {
	return next.Key != current.Key ||
		next.Driver != current.Driver ||
		next.Model != current.Model ||
		next.Endpoint != current.Endpoint ||
		next.ReasoningEffort != current.ReasoningEffort ||
		next.Parser != current.Parser ||
		next.MaxTokens != current.MaxTokens
}

func (o *Orchestrator) reloadConfigIfChanged(ctx context.Context) {
	if o.cfgPath == "" {
		return
	}
	info, err := os.Stat(o.cfgPath)
	if err != nil {
		return
	}
	mod := info.ModTime()
	o.mu.RLock()
	last := o.cfgModTime
	o.mu.RUnlock()
	if !mod.After(last) {
		return
	}
	next, err := config.Load(o.cfgPath)
	if err != nil {
		return
	}
	if err := o.applyConfig(next); err != nil {
		return
	}
	o.mu.Lock()
	o.cfgModTime = mod
	o.mu.Unlock()
	_ = ctx
}

func (o *Orchestrator) StartConfigWatcher(ctx context.Context) {
	if o.cfgPath == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				o.reloadConfigIfChanged(ctx)
			}
		}
	}()
}

func (o *Orchestrator) SlashSuggest(query string) []config.CommandEntry {
	snap := o.snapshot()
	return snap.cfg.Commands.SuggestWithProviders(query, snap.cfg.LLMBridge.Providers)
}

func (o *Orchestrator) handleModel(ctx context.Context, out chan<- bridge.StreamEvent, sessionID, message string) bool {
	parts := strings.Fields(message)
	if len(parts) < 2 {
		snap := o.snapshot()
		keys := make([]string, 0, len(snap.cfg.LLMBridge.Providers))
		for _, provider := range snap.cfg.LLMBridge.Providers {
			keys = append(keys, provider.Key)
		}
		return o.emit(ctx, out, responseEvent(sessionID, "Usage: /model <key>. Available: "+strings.Join(keys, ", ")))
	}
	key := parts[1]
	raw, err := config.LoadRaw(o.cfgPath)
	if err != nil {
		return o.emit(ctx, out, errorEvent(sessionID, err))
	}
	llm, _ := raw["llmBridge"].(map[string]any)
	if llm == nil {
		llm = map[string]any{}
		raw["llmBridge"] = llm
	}
	llm["providerKey"] = key
	b, _ := json.Marshal(raw)
	var next config.Config
	if err := json.Unmarshal(b, &next); err != nil {
		return o.emit(ctx, out, errorEvent(sessionID, err))
	}
	if next.Runtime.DataDir == "" {
		next.Runtime = o.snapshot().cfg.Runtime
	}
	if err := config.SaveRaw(o.cfgPath, raw, "slash:model"); err != nil {
		return o.emit(ctx, out, errorEvent(sessionID, err))
	}
	next, err = config.Load(o.cfgPath)
	if err != nil {
		return o.emit(ctx, out, errorEvent(sessionID, err))
	}
	if err := o.applyConfig(next); err != nil {
		return o.emit(ctx, out, errorEvent(sessionID, err))
	}
	info, _ := os.Stat(o.cfgPath)
	if info != nil {
		o.mu.Lock()
		o.cfgModTime = info.ModTime()
		o.mu.Unlock()
	}
	entry := o.snapshot().entry
	return o.emit(ctx, out, responseEvent(sessionID, fmt.Sprintf("Model switched to %s (%s · %s).", entry.Key, entry.Driver, entry.Model)))
}

// handleThinking sets the reasoning-effort level on the active provider entry
// inside llmBridge.providers and persists it to config.json. With no argument
// it reports the current level. Accepted levels: low, medium, high, off.
func (o *Orchestrator) handleThinking(ctx context.Context, out chan<- bridge.StreamEvent, sessionID, message string) bool {
	parts := strings.Fields(message)
	if len(parts) < 2 {
		entry := o.snapshot().entry
		current := strings.TrimSpace(entry.ReasoningEffort)
		if current == "" {
			current = "default (provider decides)"
		}
		return o.emit(ctx, out, responseEvent(sessionID, fmt.Sprintf("Thinking level: %s. Usage: /thinking <%s>.", current, strings.Join(config.ThinkingLevels, "|"))))
	}

	level := strings.ToLower(parts[1])
	switch level {
	case "off", "none", "disabled":
		level = ""
	case "low", "medium", "high":
		// valid
	default:
		return o.emit(ctx, out, responseEvent(sessionID, fmt.Sprintf("Unknown thinking level %q. Use one of: %s.", parts[1], strings.Join(config.ThinkingLevels, ", "))))
	}

	raw, err := config.LoadRaw(o.cfgPath)
	if err != nil {
		return o.emit(ctx, out, errorEvent(sessionID, err))
	}
	llm, _ := raw["llmBridge"].(map[string]any)
	if llm == nil {
		return o.emit(ctx, out, errorEvent(sessionID, fmt.Errorf("llmBridge config missing")))
	}
	providerKey, _ := llm["providerKey"].(string)
	providers, _ := llm["providers"].([]any)
	if len(providers) == 0 {
		return o.emit(ctx, out, errorEvent(sessionID, fmt.Errorf("no providers configured")))
	}
	applied := false
	for _, p := range providers {
		entry, ok := p.(map[string]any)
		if !ok {
			continue
		}
		key, _ := entry["key"].(string)
		// Match the active provider; if no providerKey is set, fall back to the
		// single/first entry.
		if (providerKey != "" && key == providerKey) || (providerKey == "" && !applied) {
			if level == "" {
				delete(entry, "reasoningEffort")
			} else {
				entry["reasoningEffort"] = level
			}
			applied = true
			if providerKey != "" {
				break
			}
		}
	}
	if !applied {
		return o.emit(ctx, out, errorEvent(sessionID, fmt.Errorf("active provider %q not found", providerKey)))
	}

	if err := config.SaveRaw(o.cfgPath, raw, "slash:thinking"); err != nil {
		return o.emit(ctx, out, errorEvent(sessionID, err))
	}
	next, err := config.Load(o.cfgPath)
	if err != nil {
		return o.emit(ctx, out, errorEvent(sessionID, err))
	}
	if err := o.applyConfig(next); err != nil {
		return o.emit(ctx, out, errorEvent(sessionID, err))
	}
	if info, _ := os.Stat(o.cfgPath); info != nil {
		o.mu.Lock()
		o.cfgModTime = info.ModTime()
		o.mu.Unlock()
	}

	entry := o.snapshot().entry
	shown := level
	if shown == "" {
		shown = "default (provider decides)"
	}
	msg := fmt.Sprintf("Thinking level set to %s for %s.", shown, entry.Key)
	if entry.Driver != "provider-bridge" && level != "" {
		msg += " Note: reasoning effort currently applies only to provider-bridge models; the active driver is " + entry.Driver + "."
	}
	return o.emit(ctx, out, responseEvent(sessionID, msg))
}

func errorEvent(sessionID string, err error) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventError)
	ev.SessionID = sessionID
	ev.Error = err.Error()
	return ev
}

var _ sync.Locker
