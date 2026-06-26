package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/bus"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/debug"
	"github.com/jahrulnr/sapaloq/internal/parse"
	"github.com/jahrulnr/sapaloq/internal/platform"
	"github.com/jahrulnr/sapaloq/internal/platform/headless"
	"github.com/jahrulnr/sapaloq/internal/privacyfilter"
	"github.com/jahrulnr/sapaloq/internal/prompts"
	"github.com/jahrulnr/sapaloq/internal/skills"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
	"github.com/jahrulnr/sapaloq/internal/vault"
)

type Orchestrator struct {
	cfgPath        string
	cfg            config.Config
	entry          config.LLMBridge
	bridge         bridge.Bridge
	bus            *bus.Bus
	progress       *asyncProgressWriter
	chat           *chatstore.Store
	vault          *vault.Writer
	memoryDir      string
	stateDir       string
	tasksDir       string
	workersDir     string
	workspaceDir   string
	workers        *workerRegistry
	mu             sync.RWMutex
	cfgModTime     time.Time
	activeMu       sync.Mutex
	active         map[string]*activeRun
	runSeq         uint64
	taskMu         sync.Mutex
	taskCancels    map[string]context.CancelFunc
	taskSignals    map[string]chan struct{}
	sessionTasks   map[string]map[string]struct{}
	schedulerMu    sync.Mutex
	scheduler      *toolJobScheduler
	controlMu      sync.Mutex
	controlSignals map[string]chan struct{}
	spokenMu       sync.Mutex
	spokenTasks    map[string]struct{}
	// autoClarifyCount bounds orchestrator self-answers per task so an
	// auto-answer ↔ re-ask loop with a confused sub-agent can't run forever.
	// Guarded by spokenMu (same terminal-path lock).
	autoClarifyCount map[string]int
	visionMu         sync.RWMutex
	vision           map[string]bool
	skillsMu         sync.RWMutex
	skills           []skills.Skill
	desktop          platform.Desktop
	prompts          *prompts.Manager
	// bgJobsReg is the in-process registry for non-blocking (fire-and-forget)
	// tool jobs. See tools_bg_jobs.go. bgJobsOnce guards its lazy init.
	bgJobsReg   *bgJobRegistry
	bgJobsOnce  sync.Once
	// redactor masks secrets in every tool result before it reaches the model,
	// logs, or egress. The AI keeps full tool access; only secret values in
	// results are scrubbed, so a model tricked into reading ~/.ssh/id_rsa or
	// .env never actually receives the secret. See internal/privacyfilter.
	// Read-only after New; concurrency-safe.
	redactor *privacyfilter.Filter
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
	// Best-effort audit log of every tool the orchestrator executes. The log
	// rotates by size (config.vault) so it never grows unbounded. If the writer
	// can't be created we proceed with a nil writer (auditTool no-ops).
	vc := cfg.Vault.WithDefaults()
	vaultWriter, _ := vault.NewWithOptions(
		filepath.Join(dirs.VaultDir, "tool-calls.jsonl"),
		vault.Options{MaxBytes: vc.MaxLogBytes, KeepFiles: vc.KeepRotatedFiles},
	)
	// Load file-driven skills (read-only context). Errors are non-fatal: a
	// missing/unreadable skills dir simply leaves the feature inert.
	//
	// The default skills ship embedded in the binary and are materialized to
	// the skills dir on first run (Seed), so a fresh install has working
	// defaults without any manual download/copy. Seeding never clobbers user
	// edits and is best-effort - a disk error must not break startup.
	var loadedSkills []skills.Skill
	if cfg.Skills.WithDefaults().Enabled {
		skillsDir := config.ExpandPath(cfg.Skills.WithDefaults().Dir)
		_ = skills.Seed(skillsDir)
		loadedSkills, _ = skills.Load(skillsDir)
	}
	// Detect the desktop adapter (notifications/DND). Falls back to headless on
	// non-Linux/headless hosts or when no real backend is registered, so this
	// never fails startup.
	pc := cfg.Platform.WithDefaults()
	desktop := platform.Detect(
		platform.Prefs{Adapter: pc.Adapter, DetectOrder: pc.DetectOrder, Fallback: pc.AllowFallback},
		platform.EnvFromOS(runtime.GOOS),
		func() platform.Desktop { return headless.New() },
	)
	// Load file-driven, replaceable system prompts (Ask/planner/agent/scribe).
	// Never fails: a disabled/missing dir still serves embedded defaults.
	promptCfg := cfg.Prompts.WithDefaults()
	promptMgr := prompts.New(config.ExpandPath(promptCfg.Dir), promptCfg.Enabled)
	o := &Orchestrator{
		cfgPath:      cfgPath,
		cfg:          cfg,
		entry:        entry,
		bridge:       b,
		bus:          eventBus,
		progress:     newAsyncProgressWriter(ProgressWriter{Dir: dirs.ProgressDir}),
		chat:         chatStore,
		vault:        vaultWriter,
		memoryDir:    dirs.MemoryDir,
		stateDir:     dirs.StateDir,
		tasksDir:     dirs.TasksDir,
		workersDir:   dirs.WorkersDir,
		workspaceDir: dirs.WorkspaceDir,
		workers:      newWorkerRegistry(dirs.WorkersDir),
		cfgModTime:   modTime,
		active:       make(map[string]*activeRun),
		taskCancels:  make(map[string]context.CancelFunc),
		taskSignals:  make(map[string]chan struct{}),
		sessionTasks: make(map[string]map[string]struct{}),
		vision:       make(map[string]bool),
		skills:       loadedSkills,
		desktop:      desktop,
		prompts:      promptMgr,
		redactor:     privacyfilter.New(),
	}
	// Seed the in-memory vision cache from config so a model previously proven
	// text-only (supportsImages:false) is skipped before we ever send an image
	// again - and a known-good one (true) isn't needlessly re-probed.
	o.seedVisionFromConfig(cfg)
	o.materializeRuntimeRoadmap()
	// Best-effort: index skill bodies into facts (kind="skill") so the
	// secondary FTS match in skillsBlock can find them. Never fatal.
	o.indexSkills(context.Background())
	// Best-effort: drain any learning events left pending from a previous run so
	// promoted facts land in memory before the first turn. Never fatal.
	_, _ = o.drainLearningQueue(context.Background(), 100)
	// Ensure the local-default execution node exists so spawns always have a
	// routable in-proc target. Best-effort.
	o.bootstrapLocalDefaultNode(context.Background())
	// A process restart loses in-memory worker goroutines. Persisted tasks that
	// still claim pending/in_progress/stopping would otherwise leave the user
	// staring at a task that can never advance.
	o.recoverOrphanedTasks()
	return o, nil
}

func (o *Orchestrator) Bus() *bus.Bus { return o.bus }

func (o *Orchestrator) toolJobs() *toolJobScheduler {
	o.schedulerMu.Lock()
	defer o.schedulerMu.Unlock()
	if o.scheduler != nil {
		return o.scheduler
	}
	root := ""
	if o.stateDir != "" {
		root = filepath.Join(o.stateDir, "tool-jobs")
	} else if o.memoryDir != "" {
		root = filepath.Join(o.memoryDir, "tool-jobs")
	}
	maxParallel := o.cfg.Orchestrator.WithDefaults().Continuation.MaxParallelTools
	var publisher interface {
		Publish(string, bridge.StreamEvent)
	}
	if o.bus != nil {
		publisher = o.bus
	}
	o.scheduler = newToolJobScheduler(root, maxParallel, publisher)
	return o.scheduler
}

// indexSkills upserts the in-memory skills into the facts store under
// kind="skill" so SearchFacts can surface them as a secondary signal. It is
// idempotent per boot: a skill id already present is left untouched. Best-effort
// - any error is ignored so indexing never disrupts startup.
func (o *Orchestrator) indexSkills(ctx context.Context) {
	if o == nil || o.chat == nil || len(o.skills) == 0 {
		return
	}
	existing, err := o.chat.RecentFacts(ctx, "skill", 1000)
	if err != nil {
		return
	}
	have := make(map[string]struct{}, len(existing))
	for _, f := range existing {
		if id, _, ok := splitSkillFact(f.Content); ok {
			have[id] = struct{}{}
		}
	}
	for _, sk := range o.skills {
		if _, ok := have[sk.ID]; ok {
			continue
		}
		content := sk.ID + "\n" + strings.Join(sk.Triggers, " ") + "\n" + sk.Body
		_, _ = o.chat.AddFact(ctx, "skill", content)
	}
}

// splitSkillFact parses a fact stored by indexSkills back into its skill id and
// trigger line. Layout: "<id>\n<triggers...>\n<body>".
func splitSkillFact(content string) (id, triggers string, ok bool) {
	parts := strings.SplitN(content, "\n", 3)
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return "", "", false
	}
	id = strings.TrimSpace(parts[0])
	if len(parts) >= 2 {
		triggers = parts[1]
	}
	return id, triggers, true
}

// auditTool appends an executed tool call to the vault audit log. It is
// best-effort: a nil writer or a write error is silently ignored so auditing
// never disrupts the main flow. source identifies the executor (e.g. "ask" or
// "subagent:planner").
func (o *Orchestrator) auditTool(sessionID, source string, call parse.ToolCall) {
	if o == nil || o.vault == nil {
		return
	}
	_ = o.vault.Append(vault.Entry{
		SessionID:    sessionID,
		Provider:     o.entry.Key,
		RawName:      call.Name,
		ResolvedName: call.Name,
		Arguments:    call.Arguments,
		Source:       source,
		Reason:       "executed",
	})
}

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
		var thinking strings.Builder
		assistant, err := o.runConversation(runCtx, snap, out, sessionID, message, messages, &thinking)
		if err != nil {
			debug.Debugf("orchestrator: conversation error session=%s err=%v", sessionID, err)
			// errStreamErrorSurfaced means runTurnLoop already emitted the
			// EventError (and an EventDone) to this same channel; re-emitting
			// would duplicate the error bubble in the widget.
			if !errors.Is(err, errStreamErrorSurfaced) {
				o.emit(runCtx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
			}
			return
		}
		// Persist reasoning as a show-only "thinking" turn before the answer so
		// it survives a restart (excluded from the LLM context window).
		if strings.TrimSpace(thinking.String()) != "" {
			_ = o.chat.AppendTurn(ctx, sessionID, "thinking", thinking.String(), 0)
		}
		_ = o.chat.AppendTurn(ctx, sessionID, "assistant", assistant.String(), estimateTextTokens(assistant.String()))
		usage, _ := o.ContextUsage(ctx, sessionID)
		_ = o.chat.SnapshotUsage(ctx, usage)
		// Flush + close the async progress drain for this session so the JSONL
		// is fully persisted and the per-session goroutine does not leak.
		if o.progress != nil {
			o.progress.Close(sessionID)
		}
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
	var thinking strings.Builder
	assistant, err := o.runConversation(ctx, snap, out, sessionID, message, messages, &thinking)
	if err != nil {
		// See the streaming path above: skip the duplicate emit when the
		// stream error was already surfaced to this channel by runTurnLoop.
		if !errors.Is(err, errStreamErrorSurfaced) {
			o.emit(ctx, out, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
		}
		return
	}
	if strings.TrimSpace(thinking.String()) != "" {
		_ = o.chat.AppendTurn(ctx, sessionID, "thinking", thinking.String(), 0)
	}
	_ = o.chat.AppendTurn(ctx, sessionID, "assistant", assistant.String(), estimateTextTokens(assistant.String()))
	usage, _ := o.ContextUsage(ctx, sessionID)
	_ = o.chat.SnapshotUsage(ctx, usage)
	if o.progress != nil {
		o.progress.Close(sessionID)
	}
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
