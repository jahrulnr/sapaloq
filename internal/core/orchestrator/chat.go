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
	id             uint64
	cancel         context.CancelFunc
	coalescer      *TranscriptCoalescer
	transcriptBase []bridge.TranscriptEntry
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

// emitChatTerminalError surfaces a failed chat run to the widget. Every
// terminal failure must be followed by EventDone so the chat_send IPC consumer
// and the widget's SendMessage promise unblock. When runTurnLoop already
// emitted EventError+EventDone (errStreamErrorSurfaced), this is a no-op.
func (o *Orchestrator) emitChatTerminalError(ctx context.Context, out chan<- bridge.StreamEvent, sessionID string, err error) {
	if err == nil || errors.Is(err, errStreamErrorSurfaced) {
		return
	}
	o.emitWidget(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventError, SessionID: sessionID, Error: err.Error(), At: time.Now().UTC()})
	o.emitWidget(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
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
		activeSessionID := sessionID
		if entry, ok := MatchRegistry(message, snap.cfg.Commands); ok {
			debug.Debugf("orchestrator: slash route id=%s session=%s", entry.ID, sessionID)
			if entry.ID == "clear" {
				if clearedID := o.handleSlash(ctx, out, activeSessionID, entry.ID, message); clearedID != "" {
					o.refreshActiveCoalescer(activeSessionID, runID)
					o.emitSlash(ctx, out, clearedID, responseEvent(clearedID, "Chat cleared in this room."))
					o.emitSessionReset(ctx, out, clearedID, runID, true)
				}
			} else if entry.ID == "reset" {
				if newID := o.handleSlash(ctx, out, activeSessionID, entry.ID, message); newID != "" {
					o.migrateActiveRun(activeSessionID, newID, runID)
					activeSessionID = newID
					o.emitSlash(ctx, out, newID, responseEvent(newID, "Session reset. Starting a fresh active chat."))
					o.emitSessionReset(ctx, out, newID, runID, true)
				}
			} else {
				o.handleSlash(ctx, out, activeSessionID, entry.ID, message)
			}
			o.emitWidget(ctx, out, activeSessionID, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: activeSessionID, At: time.Now().UTC()})
			return
		}
		genStr := fmt.Sprintf("%d", runID)
		_, _ = o.chat.AppendTurnIDWithGeneration(ctx, sessionID, "user", message, estimateTextTokens(message), genStr)
		o.refreshActiveTranscriptBase(ctx, sessionID)
		o.emitWidget(ctx, out, sessionID, bridge.StreamEvent{
			Kind:         bridge.EventTurnBoundary,
			SessionID:    sessionID,
			GenerationID: genStr,
			At:           time.Now().UTC(),
		})
		messages, err := o.contextMessages(ctx, sessionID, message)
		if err != nil {
			o.emitChatTerminalError(ctx, out, sessionID, err)
			return
		}
		var thinking strings.Builder
		assistant, err := o.runConversationWithGeneration(runCtx, snap, out, sessionID, genStr, message, messages, &thinking)
		if err != nil {
			debug.Debugf("orchestrator: conversation error session=%s err=%v", sessionID, err)
			o.emitChatTerminalError(runCtx, out, sessionID, err)
			return
		}
		// Persist reasoning as a show-only "thinking" turn before the answer so
		// it survives a restart (excluded from the LLM context window).
		if strings.TrimSpace(thinking.String()) != "" {
			_, _ = o.chat.AppendTurnIDWithGeneration(ctx, sessionID, "thinking", thinking.String(), 0, genStr)
		}
		// Assistant turns are persisted per inference round inside runTurnLoop.
		// Only append a final blob when the run produced visible text that was not
		// already recorded (e.g. a single tool-less answer).
		if strings.TrimSpace(assistant.String()) != "" {
			turns, _ := o.chat.ActiveTurns(ctx, sessionID, false)
			needsFinal := true
			for i := len(turns) - 1; i >= 0; i-- {
				if turns[i].Role == "assistant" && turns[i].GenerationID == genStr {
					needsFinal = false
					break
				}
			}
			if needsFinal {
				_, _ = o.chat.AppendTurnIDWithGeneration(ctx, sessionID, "assistant", assistant.String(), estimateTextTokens(assistant.String()), genStr)
			}
		}
		usage, _ := o.ContextUsage(ctx, sessionID)
		_ = o.chat.SnapshotUsage(ctx, usage)
		// Flush + close the async progress drain for this session so the JSONL
		// is fully persisted and the per-session goroutine does not leak.
		if o.progress != nil {
			o.progress.Close(sessionID)
		}
		o.emitWidget(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
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
		o.emitChatTerminalError(ctx, out, sessionID, err)
		return
	}
	var thinking strings.Builder
	genStr := fmt.Sprintf("%d", runID)
	assistant, err := o.runConversationWithGeneration(ctx, snap, out, sessionID, genStr, message, messages, &thinking)
	if err != nil {
		o.emitChatTerminalError(ctx, out, sessionID, err)
		return
	}
	if strings.TrimSpace(thinking.String()) != "" {
		_, _ = o.chat.AppendTurnIDWithGeneration(ctx, sessionID, "thinking", thinking.String(), 0, genStr)
	}
	if strings.TrimSpace(assistant.String()) != "" {
		turns, _ := o.chat.ActiveTurns(ctx, sessionID, false)
		needsFinal := true
		for i := len(turns) - 1; i >= 0; i-- {
			if turns[i].Role == "assistant" && turns[i].GenerationID == genStr {
				needsFinal = false
				break
			}
		}
		if needsFinal {
			_, _ = o.chat.AppendTurnIDWithGeneration(ctx, sessionID, "assistant", assistant.String(), estimateTextTokens(assistant.String()), genStr)
		}
	}
	usage, _ := o.ContextUsage(ctx, sessionID)
	_ = o.chat.SnapshotUsage(ctx, usage)
	if o.progress != nil {
		o.progress.Close(sessionID)
	}
	o.emitWidget(ctx, out, sessionID, bridge.StreamEvent{Kind: bridge.EventDone, SessionID: sessionID, At: time.Now().UTC()})
}

func (o *Orchestrator) setActiveGeneration(sessionID string, cancel context.CancelFunc) uint64 {
	o.activeMu.Lock()
	o.runSeq++
	runID := o.runSeq
	if previous := o.active[sessionID]; previous != nil {
		previous.cancel()
	}
	genStr := fmt.Sprintf("%d", runID)
	o.active[sessionID] = &activeRun{
		id:        runID,
		cancel:    cancel,
		coalescer: NewTranscriptCoalescer(genStr),
	}
	o.activeMu.Unlock()
	return runID
}

func (o *Orchestrator) activeGenerationString(sessionID string) string {
	o.activeMu.Lock()
	defer o.activeMu.Unlock()
	if run := o.active[sessionID]; run != nil {
		return fmt.Sprintf("%d", run.id)
	}
	return ""
}

func (o *Orchestrator) activeCoalescer(sessionID string) *TranscriptCoalescer {
	o.activeMu.Lock()
	defer o.activeMu.Unlock()
	if run := o.active[sessionID]; run != nil {
		return run.coalescer
	}
	return nil
}

func (o *Orchestrator) refreshActiveTranscriptBase(ctx context.Context, sessionID string) {
	base, err := o.SessionTranscript(ctx, sessionID)
	if err != nil {
		return
	}
	o.activeMu.Lock()
	if run := o.active[sessionID]; run != nil {
		run.transcriptBase = base
	}
	o.activeMu.Unlock()
}

func (o *Orchestrator) activeTranscriptBase(sessionID string) []bridge.TranscriptEntry {
	o.activeMu.Lock()
	defer o.activeMu.Unlock()
	run := o.active[sessionID]
	if run == nil || len(run.transcriptBase) == 0 {
		return nil
	}
	out := make([]bridge.TranscriptEntry, len(run.transcriptBase))
	copy(out, run.transcriptBase)
	return out
}

func (o *Orchestrator) clearActiveGeneration(sessionID string, runID uint64) {
	o.activeMu.Lock()
	if current := o.active[sessionID]; current != nil && current.id == runID {
		delete(o.active, sessionID)
	}
	o.activeMu.Unlock()
}

// migrateActiveRun moves the in-flight generation/coalescer from one session id
// to another (e.g. after /reset creates a fresh chat session mid-request).
func (o *Orchestrator) migrateActiveRun(oldID, newID string, runID uint64) {
	if oldID == "" || newID == "" || oldID == newID {
		return
	}
	o.activeMu.Lock()
	defer o.activeMu.Unlock()
	run := o.active[oldID]
	if run == nil || run.id != runID {
		return
	}
	delete(o.active, oldID)
	genStr := fmt.Sprintf("%d", runID)
	o.active[newID] = &activeRun{
		id:        runID,
		cancel:    run.cancel,
		coalescer: NewTranscriptCoalescer(genStr),
	}
}

// refreshActiveCoalescer drops the live transcript coalescer for an in-place
// clear (/clear) while keeping the same session id and generation run.
func (o *Orchestrator) refreshActiveCoalescer(sessionID string, runID uint64) {
	o.activeMu.Lock()
	defer o.activeMu.Unlock()
	run := o.active[sessionID]
	if run == nil || run.id != runID {
		return
	}
	run.coalescer = NewTranscriptCoalescer(fmt.Sprintf("%d", runID))
}

// emitSlash forwards slash-command output through the widget transcript path.
func (o *Orchestrator) emitSlash(ctx context.Context, out chan<- bridge.StreamEvent, sessionID string, ev bridge.StreamEvent) bool {
	return o.emitWidget(ctx, out, sessionID, ev)
}

// emitSessionReset tells the widget to discard the current transcript and render
// the supplied snapshot. Only call after the backend has persisted the reset.
func (o *Orchestrator) emitSessionReset(ctx context.Context, out chan<- bridge.StreamEvent, sessionID string, runID uint64, finished bool) bool {
	genStr := fmt.Sprintf("%d", runID)
	var entries []bridge.TranscriptEntry
	if coalescer := o.activeCoalescer(sessionID); coalescer != nil {
		entries = coalescer.Entries()
	}
	if o.chat != nil {
		if live, err := o.LiveSessionTranscript(ctx, sessionID, o.activeCoalescer(sessionID)); err == nil && len(live) > 0 {
			entries = live
		}
	}
	patch := bridge.TranscriptPatch{
		SessionID:    sessionID,
		GenerationID: genStr,
		Entries:      entries,
		Reset:        true,
		Finished:     finished,
	}
	patchEv := bridge.StreamEvent{
		Kind:         bridge.EventTranscript,
		SessionID:    sessionID,
		GenerationID: genStr,
		Transcript:   &patch,
		At:           time.Now().UTC(),
	}
	return o.sendOut(ctx, out, patchEv)
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
	return o.sendOut(ctx, out, ev)
}

// emitWidget logs the raw event, updates the live coalescer, and forwards only
// transcript patches to the widget channel (full replace migration).
func (o *Orchestrator) emitWidget(ctx context.Context, out chan<- bridge.StreamEvent, sessionID string, ev bridge.StreamEvent) bool {
	if sessionID != "" {
		ev.SessionID = sessionID
	}
	genID := o.activeGenerationString(sessionID)
	if genID != "" {
		ev.GenerationID = genID
	}
	_ = o.progress.Append(ev.SessionID, ev)
	if o.bus != nil {
		o.bus.Publish(topicFor(ev.Kind), ev)
	}
	coalescer := o.activeCoalescer(sessionID)
	scratch := coalescer
	if scratch == nil && widgetCoalesceKind(ev.Kind) {
		scratch = NewTranscriptCoalescer(genID)
	}
	if scratch != nil && widgetCoalesceKind(ev.Kind) {
		scratch.Apply(ev)
	}
	if !widgetPatchKind(ev.Kind) {
		return true
	}
	var entries []bridge.TranscriptEntry
	if coalescer != nil {
		switch ev.Kind {
		case bridge.EventResponseDelta, bridge.EventThinkingDelta:
			base := o.activeTranscriptBase(sessionID)
			if base == nil {
				base, _ = o.SessionTranscript(ctx, sessionID)
			}
			entries = mergeLiveTranscript(base, coalescer.EntriesWithPending())
		default:
			if ev.Kind == bridge.EventTurnBoundary || ev.Kind == bridge.EventCheckpoint ||
				ev.Kind == bridge.EventToolUpdate || ev.Kind == bridge.EventToolCall {
				o.refreshActiveTranscriptBase(ctx, sessionID)
			}
			base := o.activeTranscriptBase(sessionID)
			if base == nil {
				entries, _ = o.LiveSessionTranscript(ctx, sessionID, coalescer)
			} else {
				entries = mergeLiveTranscript(base, coalescer.EntriesWithPending())
			}
		}
	} else if scratch != nil {
		entries = scratch.Entries()
	}
	finished := ev.Kind == bridge.EventError || ev.Kind == bridge.EventDone
	patch := bridge.TranscriptPatch{
		SessionID:    sessionID,
		GenerationID: genID,
		Entries:      entries,
		Finished:     finished,
	}
	if ev.Kind != bridge.EventResponseDelta && ev.Kind != bridge.EventThinkingDelta && o.chat != nil {
		if u, err := o.ContextUsage(ctx, sessionID); err == nil {
			patch.Usage = &bridge.TranscriptUsage{
				UsedTokens:    u.UsedTokens,
				ContextWindow: u.ContextWindow,
				Percent:       u.Percent,
				Provider:      u.Provider,
				Model:         u.Model,
			}
		}
	}
	patchEv := bridge.StreamEvent{
		Kind:         bridge.EventTranscript,
		SessionID:    sessionID,
		GenerationID: genID,
		Transcript:   &patch,
		At:           time.Now().UTC(),
	}
	return o.sendOut(ctx, out, patchEv)
}

func (o *Orchestrator) sendOut(ctx context.Context, out chan<- bridge.StreamEvent, ev bridge.StreamEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}

func widgetCoalesceKind(kind bridge.EventKind) bool {
	switch kind {
	case bridge.EventThinkingDelta, bridge.EventResponseDelta, bridge.EventToolCall,
		bridge.EventToolUpdate, bridge.EventStatus, bridge.EventTurnBoundary,
		bridge.EventTaskUpdate, bridge.EventError, bridge.EventCheckpoint, bridge.EventDone:
		return true
	default:
		return false
	}
}

func widgetPatchKind(kind bridge.EventKind) bool {
	switch kind {
	case bridge.EventThinkingDelta, bridge.EventResponseDelta, bridge.EventToolCall,
		bridge.EventToolUpdate, bridge.EventStatus, bridge.EventTurnBoundary,
		bridge.EventTaskUpdate, bridge.EventError, bridge.EventCheckpoint,
		bridge.EventDone, bridge.EventTranscript:
		return true
	default:
		return false
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
