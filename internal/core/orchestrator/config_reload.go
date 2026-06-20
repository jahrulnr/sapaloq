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
	o.progress = ProgressWriter{Dir: dirs.ProgressDir}
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
	return next.Key != current.Key || next.Driver != current.Driver || next.Model != current.Model || next.Endpoint != current.Endpoint
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

func errorEvent(sessionID string, err error) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventError)
	ev.SessionID = sessionID
	ev.Error = err.Error()
	return ev
}

var _ sync.Locker
