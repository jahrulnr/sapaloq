package orchestrator

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

type Orchestrator struct {
	cfgPath      string
	cfg          config.Config
	entry        config.LLMBridge
	bridge       bridge.Bridge
	bus          *bus.Bus
	progress     ProgressWriter
	chat         *chatstore.Store
	memoryDir    string
	mu           sync.RWMutex
	cfgModTime   time.Time
	activeMu     sync.Mutex
	active       map[string]*activeRun
	runSeq       uint64
	taskMu       sync.Mutex
	taskCancels  map[string]context.CancelFunc
	taskSignals  map[string]chan struct{}
	sessionTasks map[string]map[string]struct{}
	visionMu     sync.RWMutex
	vision       map[string]bool
}

type activeRun struct {
	id     uint64
	cancel context.CancelFunc
}

func New(cfg config.Config, cfgPath string, b bridge.Bridge, eventBus *bus.Bus) (*Orchestrator, error) {
	entry, err := cfg.LLMBridge.ActiveProvider()
	if err != nil {
		return nil, err
	}
	dirs := config.RuntimeDirs(cfg)
	chatStore, err := chatstore.Open(dirs.MemoryDir)
	if err != nil {
		return nil, fmt.Errorf("chat store: %w", err)
	}
	var modTime time.Time
	if cfgPath != "" {
		if info, statErr := os.Stat(cfgPath); statErr == nil {
			modTime = info.ModTime()
		}
	}
	return &Orchestrator{
		cfgPath:      cfgPath,
		cfg:          cfg,
		entry:        entry,
		bridge:       b,
		bus:          eventBus,
		progress:     ProgressWriter{Dir: dirs.ProgressDir},
		chat:         chatStore,
		memoryDir:    dirs.MemoryDir,
		cfgModTime:   modTime,
		active:       make(map[string]*activeRun),
		taskCancels:  make(map[string]context.CancelFunc),
		taskSignals:  make(map[string]chan struct{}),
		sessionTasks: make(map[string]map[string]struct{}),
		vision:       make(map[string]bool),
	}, nil
}

func (o *Orchestrator) Bus() *bus.Bus { return o.bus }

func (o *Orchestrator) SendChat(ctx context.Context, sessionID, message string) (<-chan bridge.StreamEvent, error) {
	o.reloadConfigIfChanged(ctx)
	snap := o.snapshot()
	if sessionID == "" {
		var err error
		sessionID, err = o.chat.ActiveSession(ctx, snap.entry.Key, snap.entry.Model)
		if err != nil {
			return nil, err
		}
	}
	out := make(chan bridge.StreamEvent, 32)
	runCtx, cancel := context.WithCancel(ctx)
	runID := o.setActiveGeneration(sessionID, cancel)
	go func() {
		defer close(out)
		defer o.clearActiveGeneration(sessionID, runID)
		defer cancel()
		if entry, ok := MatchRegistry(message, snap.cfg.Commands); ok {
			debug.Debugf("orchestrator: slash route id=%s session=%s", entry.ID, sessionID)
			o.handleSlash(ctx, out, sessionID, entry.ID, message)
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
			return
		}
		_ = o.chat.AppendTurn(ctx, sessionID, "user", message, estimateTextTokens(message))
		messages, err := o.contextMessages(ctx, sessionID, message)
		if err != nil {
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return
		}
		assistant, err := o.runConversation(runCtx, snap, out, sessionID, message, messages)
		if err != nil {
			debug.Debugf("orchestrator: conversation error session=%s err=%v", sessionID, err)
			o.emit(runCtx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			return
		}
		_ = o.chat.AppendTurn(ctx, sessionID, "assistant", assistant.String(), estimateTextTokens(assistant.String()))
		usage, _ := o.ContextUsage(ctx, sessionID)
		_ = o.chat.SnapshotUsage(ctx, usage)
	}()
	return out, nil
}

// RetryChat regenerates the response for an existing user turn. It preserves
// that user turn, removes only its descendants, and does not append a duplicate
// user message.
func (o *Orchestrator) RetryChat(ctx context.Context, sessionID string, turnID int64) (<-chan bridge.StreamEvent, error) {
	if turnID <= 0 {
		return nil, fmt.Errorf("turn id is required")
	}
	o.reloadConfigIfChanged(ctx)
	snap := o.snapshot()
	if sessionID == "" {
		var err error
		sessionID, err = o.chat.ActiveSession(ctx, snap.entry.Key, snap.entry.Model)
		if err != nil {
			return nil, err
		}
	}
	turn, err := o.chat.Turn(ctx, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	if turn.Role != "user" {
		return nil, fmt.Errorf("turn %d is not a user message", turnID)
	}
	if err := o.chat.DeleteAfterTurn(ctx, sessionID, turnID); err != nil {
		return nil, err
	}
	out := make(chan bridge.StreamEvent, 32)
	runCtx, cancel := context.WithCancel(ctx)
	runID := o.setActiveGeneration(sessionID, cancel)
	go o.completeExistingTurn(runCtx, cancel, runID, snap, out, sessionID, turn.Content)
	return out, nil
}

func (o *Orchestrator) completeExistingTurn(ctx context.Context, cancel context.CancelFunc, runID uint64, snap providerSnapshot, out chan bridge.StreamEvent, sessionID, message string) {
	defer close(out)
	defer o.clearActiveGeneration(sessionID, runID)
	defer cancel()
	messages, err := o.contextMessages(ctx, sessionID, message)
	if err != nil {
		o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
		return
	}
	assistant, err := o.runConversation(ctx, snap, out, sessionID, message, messages)
	if err != nil {
		o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
		return
	}
	_ = o.chat.AppendTurn(ctx, sessionID, "assistant", assistant.String(), estimateTextTokens(assistant.String()))
	usage, _ := o.ContextUsage(ctx, sessionID)
	_ = o.chat.SnapshotUsage(ctx, usage)
}

func (o *Orchestrator) setActiveGeneration(sessionID string, cancel context.CancelFunc) uint64 {
	o.activeMu.Lock()
	o.runSeq++
	runID := o.runSeq
	if previous := o.active[sessionID]; previous != nil {
		previous.cancel()
	}
	o.active[sessionID] = &activeRun{id: runID, cancel: cancel}
	o.activeMu.Unlock()
	return runID
}

func (o *Orchestrator) clearActiveGeneration(sessionID string, runID uint64) {
	o.activeMu.Lock()
	if current := o.active[sessionID]; current != nil && current.id == runID {
		delete(o.active, sessionID)
	}
	o.activeMu.Unlock()
}

func (o *Orchestrator) StopChat(sessionID string) bool {
	stopped, _ := o.Stop(sessionID, "generation", "")
	return stopped
}

func (o *Orchestrator) Stop(sessionID, scope, taskID string) (bool, string) {
	if scope == "" {
		scope = "generation"
	}
	stopped := false
	if scope == "generation" || scope == "all" {
		o.activeMu.Lock()
		run := o.active[sessionID]
		o.activeMu.Unlock()
		if run != nil {
			run.cancel()
			stopped = true
		}
	}
	if scope == "task" {
		if o.stopTask(taskID) {
			return true, "task stopped"
		}
		return stopped, "no active task"
	}
	if scope == "all" {
		for _, id := range o.tasksForSession(sessionID) {
			if o.stopTask(id) {
				stopped = true
			}
		}
	}
	if scope != "generation" && scope != "all" {
		return false, "invalid stop scope"
	}
	if stopped {
		return true, scope + " stopped"
	}
	return false, "no active " + scope
}

func (o *Orchestrator) tasksForSession(sessionID string) []string {
	o.taskMu.Lock()
	defer o.taskMu.Unlock()
	var ids []string
	for id := range o.sessionTasks[sessionID] {
		ids = append(ids, id)
	}
	return ids
}

func (o *Orchestrator) stopTask(taskID string) bool {
	if taskID == "" {
		return false
	}
	o.taskMu.Lock()
	cancel := o.taskCancels[taskID]
	o.taskMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	if record, err := o.readTask(taskID); err == nil && !taskTerminal(record.Status) {
		record.Status = "stopping"
		record.UpdatedAt = time.Now().UTC()
		_ = o.writeTask(record)
	}
	return true
}

func (o *Orchestrator) emit(ctx context.Context, out chan<- bridge.StreamEvent, ev bridge.StreamEvent) bool {
	_ = o.progress.Append(ev.SessionID, ev)
	if o.bus != nil {
		o.bus.Publish(topicFor(ev.Kind), ev)
	}
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}

func settingsEvent(sessionID, id string) bridge.StreamEvent {
	ev := bridge.NewEvent(bridge.EventResponseDelta)
	ev.SessionID = sessionID
	ev.Delta = fmt.Sprintf("/%s handler is registered; config patch sub-agent is TODO for MVP.", id)
	return ev
}

func topicFor(kind bridge.EventKind) string {
	switch kind {
	case bridge.EventThinkingDelta:
		return "sapaloq.v1.chat.thinking"
	case bridge.EventResponseDelta:
		return "sapaloq.v1.chat.response"
	case bridge.EventToolCall:
		return "sapaloq.v1.chat.tool_call"
	case bridge.EventStatus:
		return "sapaloq.v1.chat.status"
	default:
		return "sapaloq.v1.chat." + string(kind)
	}
}
