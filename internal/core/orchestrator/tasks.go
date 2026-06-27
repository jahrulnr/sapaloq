package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

type taskRecord struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	Task      string `json:"task"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	// PlanTaskID references the planner task whose plan.md this agent executes
	// (set when Ask spawns an agent after a plan). Empty for direct agents.
	PlanTaskID string `json:"plan_task_id,omitempty"`
	// Question holds the pending clarification text when Status is
	// awaiting_clarification.
	Question string `json:"question,omitempty"`
	// Transcript is read only by the one-shot legacy migration. New actor turns
	// are stored in turns.json beside this status file.
	Transcript []taskTurn `json:"transcript,omitempty"`
	// Answer is the user's clarification answer, set transiently on resume and
	// consumed by buildSubAgentMessages as the resume nudge.
	Answer string `json:"answer,omitempty"`
	// Node is the execution node this task was routed to (observability).
	// "local-default" (or empty) means in-proc execution.
	Node        string     `json:"node,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// taskTurn is one persisted sub-agent turn used to resume a paused task. Only
// role+content (no images) are stored to keep status.json small.
type taskTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// maxTranscriptTurnBytes caps the content stored per transcript turn so a
// paused task's status.json stays bounded.
const maxTranscriptTurnBytes = 8 * 1024

func (o *Orchestrator) dispatchAskTool(ctx context.Context, snap providerSnapshot, out chan<- bridge.StreamEvent, sessionID, fallbackTask string, call parse.ToolCall, args toolArgs) turnOutcome {
	switch call.Name {
	case "sapaloq_spawn_plan":
		task := strings.TrimSpace(args.Task)
		if task == "" {
			task = fallbackTask
		}
		task = carryImageAttachments(task, fallbackTask)
		id, err := o.spawnBackground(snap, sessionID, "planner", task, "")
		if err != nil {
			return turnOutcome{text: "Failed to start planner: " + err.Error(), handled: true}
		}
		return turnOutcome{text: fmt.Sprintf("Planner started in background (`%s`).", id), handled: true}
	case "sapaloq_spawn_agent":
		task := strings.TrimSpace(args.Task)
		if task == "" {
			task = fallbackTask
		}
		task = carryImageAttachments(task, fallbackTask)
		planTaskID := strings.TrimSpace(args.PlanTaskID)
		if planTaskID != "" {
			if err := o.validatePlanForAgent(sessionID, planTaskID); err != nil {
				return turnOutcome{text: "Cannot use plan: " + err.Error(), handled: true}
			}
		}
		id, err := o.spawnBackground(snap, sessionID, "task-runner", task, planTaskID)
		if err != nil {
			return turnOutcome{text: "Failed to start agent: " + err.Error(), handled: true}
		}
		msg := fmt.Sprintf("Agent started in background (`%s`).", id)
		if planTaskID != "" {
			msg += fmt.Sprintf(" Using plan from `%s`.", planTaskID)
		}
		return turnOutcome{text: msg, handled: true}
	case "sapaloq_spawn_scribe":
		task := strings.TrimSpace(args.Task)
		if task == "" {
			task = fallbackTask
		}
		id, err := o.spawnBackground(snap, sessionID, "scribe", task, "")
		if err != nil {
			return turnOutcome{text: "Failed to start scribe: " + err.Error(), handled: true}
		}
		return turnOutcome{text: fmt.Sprintf("Scribe started in background (`%s`).", id), handled: true}
	case "sapaloq_get_task_status":
		taskID := strings.TrimSpace(args.TaskID)
		if taskID == "" {
			taskID = o.latestTaskID()
		}
		record, err := o.readTask(taskID)
		if err != nil {
			return turnOutcome{text: "Task status unavailable: " + err.Error(), handled: true}
		}
		response := fmt.Sprintf("Task `%s` is **%s**.", record.ID, record.Status)
		if record.Question != "" {
			response += "\n\nNeeds clarification: " + record.Question
		}
		if record.Result != "" {
			response += "\n\n" + record.Result
		}
		if record.Error != "" {
			response += "\n\nError: " + record.Error
		}
		return turnOutcome{text: response, handled: true}
	case "wait":
		mode := strings.TrimSpace(args.Mode)
		if mode == "" {
			mode = "time"
		}
		switch mode {
		case "time":
			waitSecs := effectiveWaitSeconds(args.Seconds, snap.cfg.Orchestrator.Continuation.MaxWaitSeconds)
			o.emit(ctx, out, waitingEvent(sessionID, waitSecs))
			sleep := time.Duration(args.Seconds) * time.Second
			if args.Seconds <= 0 {
				sleep = time.Duration(waitSecs) * time.Second
			}
			if maxWait := snap.cfg.Orchestrator.Continuation.MaxWaitSeconds; maxWait > 0 && sleep > time.Duration(maxWait)*time.Second {
				sleep = time.Duration(maxWait) * time.Second
			}
			select {
			case <-ctx.Done():
				return turnOutcome{text: "Wait cancelled.", handled: true, stop: true}
			case <-time.After(sleep):
			}
			o.emit(ctx, out, statusEvent(sessionID, "working"))
			return turnOutcome{text: fmt.Sprintf("Waited %ds.", int(sleep.Seconds())), handled: true}
		case "tool":
			jobID := strings.TrimSpace(args.JobID)
			if jobID == "" {
				return turnOutcome{text: "Error: job_id is required for wait mode=tool.", handled: true}
			}
			timeout := time.Duration(args.TimeoutSeconds) * time.Second
			if args.TimeoutSeconds < 0 {
				timeout = 30 * time.Second
			}
			if args.TimeoutSeconds == 0 {
				timeout = 0
			}
			if timeout > 300*time.Second {
				timeout = 300 * time.Second
			}
			reg := o.bgJobs()
			if reg == nil {
				return turnOutcome{text: "Error: background job registry unavailable.", handled: true}
			}
			start := time.Now()
			done, snap := reg.wait(jobID, timeout)
			elapsed := time.Since(start)
			view := bgJobToView(snap)
			view["waited_ms"] = elapsed.Milliseconds()
			if !done {
				view["hint"] = "job is still running. Either call wait again with a larger timeout_seconds, sapaloq_cancel_job(job_id) to abort, or sapaloq_fail_task if it has been too long."
			} else {
				delete(view, "hint")
			}
			raw, err := json.Marshal(view)
			if err != nil {
				return turnOutcome{text: fmt.Sprintf("Error: marshal wait result: %v", err), handled: true}
			}
			return turnOutcome{text: string(raw), handled: true}
		case "task":
			taskID := strings.TrimSpace(args.TaskID)
			if taskID == "" {
				taskID = o.latestTaskID()
			}
			waitSecs := effectiveWaitSeconds(args.Seconds, snap.cfg.Orchestrator.Continuation.MaxWaitSeconds)
			o.emit(ctx, out, waitingEvent(sessionID, waitSecs))
			record, changed, err := o.waitForTaskChange(ctx, taskID, args.Seconds, snap.cfg.Orchestrator.Continuation.MaxWaitSeconds)
			if err != nil {
				if ctx.Err() != nil {
					return turnOutcome{text: "Wait cancelled.", handled: true, stop: true}
				}
				return turnOutcome{text: "Wait failed: " + err.Error(), handled: true}
			}
			o.emit(ctx, out, statusEvent(sessionID, "working"))
			if !changed {
				return turnOutcome{text: fmt.Sprintf(
					"Task `%s` masih %s setelah jendela tunggu. Tidak perlu menunggu - aku akan otomatis mengabari di chat begitu task selesai/gagal/butuh keputusan (kamu juga bisa cek `sapaloq_get_task_status`).",
					record.ID, record.Status), handled: true}
			}
			response := fmt.Sprintf("Task `%s` changed to **%s**.", record.ID, record.Status)
			if record.Question != "" {
				response += "\n\nNeeds clarification: " + record.Question
			}
			if record.Result != "" {
				response += "\n\n" + record.Result
			}
			if record.Error != "" {
				response += "\n\nError: " + record.Error
			}
			return turnOutcome{text: response, handled: true}
		case "events":
			timeout := args.TimeoutSeconds
			if timeout <= 0 {
				timeout = 120
			}
			events := o.waitActorEvents(ctx, sessionID, time.Duration(timeout)*time.Second)
			if len(events) == 0 {
				return turnOutcome{text: "No actor event arrived before the wait ended.", handled: true}
			}
			return turnOutcome{text: actorEventsPrompt(events), handled: true}
		default:
			return turnOutcome{text: "Error: unknown wait mode " + mode + " (use time|tool|task|events).", handled: true}
		}
	case "sapaloq_cancel_job":
		jobID := strings.TrimSpace(args.JobID)
		if jobID == "" {
			return turnOutcome{text: "Error: job_id is required.", handled: true}
		}
		reg := o.bgJobs()
		if reg == nil {
			return turnOutcome{text: "Error: background job registry unavailable.", handled: true}
		}
		snap, ok := reg.cancel(jobID)
		if !ok {
			return turnOutcome{text: fmt.Sprintf("Error: job_id %q not found.", jobID), handled: true}
		}
		view := bgJobToView(snap)
		raw, err := json.Marshal(view)
		if err != nil {
			return turnOutcome{text: fmt.Sprintf("Error: marshal cancel: %v", err), handled: true}
		}
		return turnOutcome{text: string(raw), handled: true}
	case "sapaloq_answer_clarification":
		answer := strings.TrimSpace(args.Answer)
		if answer == "" {
			return turnOutcome{text: "Error: answer is required.", handled: true}
		}
		taskID := strings.TrimSpace(args.TaskID)
		if taskID == "" {
			taskID = o.latestAwaitingTaskID(sessionID)
		}
		if taskID == "" {
			return turnOutcome{text: "No task is awaiting clarification.", handled: true}
		}
		record, err := o.readTask(taskID)
		if err != nil {
			return turnOutcome{text: "Task unavailable: " + err.Error(), handled: true}
		}
		if record.Status != "awaiting_clarification" {
			return turnOutcome{text: fmt.Sprintf("Task `%s` is %s, not awaiting clarification; cannot answer.", record.ID, record.Status), handled: true}
		}
		record.Answer = answer
		record.Question = ""
		record.Status = "in_progress"
		record.UpdatedAt = time.Now().UTC()
		if err := o.writeTask(record); err != nil {
			return turnOutcome{text: "Failed to persist answer: " + err.Error(), handled: true}
		}
		o.resumeBackground(snap, sessionID, record)
		return turnOutcome{text: fmt.Sprintf("Answer delivered; task `%s` resumed in the background.", record.ID), handled: true}
	case "sapaloq_stop":
		reason := strings.TrimSpace(args.Reason)
		if reason == "" {
			reason = "model requested the continuation to stop"
		}
		scope := strings.TrimSpace(args.Scope)
		if scope == "" || scope == "generation" {
			return turnOutcome{text: "Stopped: " + reason, handled: true, stop: true}
		}
		if scope == "task" {
			stopped := false
			if taskID := strings.TrimSpace(args.TaskID); taskID != "" {
				stopped = o.stopTask(taskID)
			}
			message := "no active task"
			if stopped {
				message = "task stopped"
			}
			// Foreground chat must end even when the named background actor is not
			// directly cancellable (already finished, wrong id, or scope=task was
			// used instead of generation). Without stop:true the autopilot nudge to
			// call sapaloq_stop loops forever while tool results reset the budget.
			return turnOutcome{text: message + ": " + reason, handled: true, stop: true}
		}
		if scope == "all" {
			stopped := 0
			for _, id := range o.tasksForSession(sessionID) {
				if o.stopTask(id) {
					stopped++
				}
			}
			return turnOutcome{text: fmt.Sprintf("generation and %d task(s) stopped: %s", stopped, reason), handled: true, stop: true}
		}
		return turnOutcome{text: "Invalid stop scope: " + scope, handled: true}
	case "sapaloq_send_steering":
		target := strings.TrimSpace(args.TargetTaskID)
		message := strings.TrimSpace(args.Message)
		if target == "" || message == "" {
			return turnOutcome{text: "Error: target_task_id and message are required.", handled: true}
		}
		err := o.enqueueActorEvent(actorControlEvent{
			Kind:          "steering.proposed",
			SessionID:     sessionID,
			SourceID:      sessionID,
			TargetID:      target,
			CorrelationID: args.CorrelationID,
			Message:       message,
			Priority:      args.Priority,
		})
		if err != nil {
			return turnOutcome{text: "Failed to queue steering: " + err.Error(), handled: true}
		}
		return turnOutcome{text: fmt.Sprintf("Steering queued for `%s`.", target), handled: true}
	case "desktop_notify":
		return turnOutcome{text: o.toolDesktopNotify(ctx, args), handled: true}
	case "desktop_dnd_status":
		return turnOutcome{text: o.toolDesktopDNDStatus(ctx), handled: true}
	default:
		return turnOutcome{}
	}
}

func (o *Orchestrator) latestTaskID() string {
	entries, err := os.ReadDir(o.tasksRoot())
	if err != nil {
		return ""
	}
	var latest os.DirEntry
	var latestTime time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil && (latest == nil || info.ModTime().After(latestTime)) {
			latest = entry
			latestTime = info.ModTime()
		}
	}
	if latest == nil {
		return ""
	}
	return latest.Name()
}

func carryImageAttachments(task, source string) string {
	if strings.Contains(task, "data:image/") || !strings.Contains(source, "data:image/") {
		return task
	}
	var attachments []string
	for _, match := range inlineImageRE.FindAllString(source, -1) {
		attachments = append(attachments, match)
	}
	if len(attachments) == 0 {
		return task
	}
	return strings.TrimSpace(task) + "\n\n" + strings.Join(attachments, "\n")
}

func (o *Orchestrator) spawnBackground(snap providerSnapshot, sessionID, role, task, planTaskID string) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("task is required")
	}
	if snap.br == nil {
		snap = o.snapshot()
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("task-%d", now.UnixNano())
	// Route the spawn to an execution node. With only local-default configured
	// this resolves to in-proc execution (unchanged behavior). The chosen node
	// name is recorded for observability.
	node := o.pickNode(context.Background(), role, "")
	record := taskRecord{ID: id, SessionID: sessionID, Role: role, Status: "pending", Task: task, PlanTaskID: planTaskID, Node: node.Name, CreatedAt: now, UpdatedAt: now}
	if o.chat != nil {
		if err := o.chat.AppendTurn(context.Background(), id, "user", task, estimateTextTokens(task)); err != nil {
			return "", fmt.Errorf("persist actor intent: %w", err)
		}
	}
	if err := o.writeTask(record); err != nil {
		return "", err
	}
	o.publishTaskUpdate(sessionID, record)
	// No hard total-runtime cap: a productive background task must be free to
	// work as long as it makes progress. The real guards are runConversation's
	// inactivity (idle) deadline - which fires only when the run goes silent -
	// the worker watchdog (stale heartbeat), and the loop-anomaly budgets. The
	// stored cancel still backs user-initiated Stop and shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	o.taskMu.Lock()
	if o.taskCancels == nil {
		o.taskCancels = make(map[string]context.CancelFunc)
	}
	if o.sessionTasks == nil {
		o.sessionTasks = make(map[string]map[string]struct{})
	}
	o.taskCancels[id] = cancel
	if o.sessionTasks[sessionID] == nil {
		o.sessionTasks[sessionID] = make(map[string]struct{})
	}
	o.sessionTasks[sessionID][id] = struct{}{}
	o.taskMu.Unlock()
	go o.runBackgroundTask(ctx, cancel, snap, sessionID, record)
	return id, nil
}

// resumeBackground re-enters the background loop for an already-persisted task
// (e.g. one paused on awaiting_clarification and now answered). It mirrors the
// goroutine plumbing of spawnBackground but reuses the existing record (with
// its transcript + answer) rather than creating a new one.
func (o *Orchestrator) resumeBackground(snap providerSnapshot, sessionID string, record taskRecord) {
	if snap.br == nil {
		snap = o.snapshot()
	}
	// Same rationale as spawnBackground: no total-runtime cap; the idle
	// deadline + worker watchdog + loop-anomaly budgets are the real guards.
	ctx, cancel := context.WithCancel(context.Background())
	o.taskMu.Lock()
	if o.taskCancels == nil {
		o.taskCancels = make(map[string]context.CancelFunc)
	}
	if o.sessionTasks == nil {
		o.sessionTasks = make(map[string]map[string]struct{})
	}
	if _, running := o.taskCancels[record.ID]; running {
		o.taskMu.Unlock()
		cancel()
		return
	}
	o.taskCancels[record.ID] = cancel
	if o.sessionTasks[sessionID] == nil {
		o.sessionTasks[sessionID] = make(map[string]struct{})
	}
	o.sessionTasks[sessionID][record.ID] = struct{}{}
	o.taskMu.Unlock()
	go o.runBackgroundTask(ctx, cancel, snap, sessionID, record)
}

// latestAwaitingTaskID returns the most recently updated task in this session
// that is currently awaiting clarification, so the user can answer without
// naming a task id.
func (o *Orchestrator) latestAwaitingTaskID(sessionID string) string {
	entries, err := os.ReadDir(o.tasksRoot())
	if err != nil {
		return ""
	}
	var bestID string
	var bestTime time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rec, readErr := o.readTask(entry.Name())
		if readErr != nil || rec.Status != "awaiting_clarification" {
			continue
		}
		if sessionID != "" && rec.SessionID != sessionID {
			continue
		}
		if rec.UpdatedAt.After(bestTime) {
			bestTime = rec.UpdatedAt
			bestID = rec.ID
		}
	}
	return bestID
}

func (o *Orchestrator) runBackgroundTask(ctx context.Context, cancel context.CancelFunc, snap providerSnapshot, sessionID string, record taskRecord) {
	defer cancel()
	// Register this worker in the live roster so its health (PID, phase,
	// heartbeat) is observable and the watchdog can detect a stall. The final
	// status is recorded on deregister for post-mortem inspection.
	o.workers.register(record.ID, record.Role, sessionID, record.Node)
	defer func() {
		o.workers.deregister(record.ID, record.Status)
		o.taskMu.Lock()
		delete(o.taskCancels, record.ID)
		if tasks := o.sessionTasks[sessionID]; tasks != nil {
			delete(tasks, record.ID)
			if len(tasks) == 0 {
				delete(o.sessionTasks, sessionID)
			}
		}
		o.taskMu.Unlock()
		// Flush + close the async progress drain for this task's JSONL so the
		// per-task goroutine does not leak and the audit log is fully persisted.
		if o.progress != nil {
			o.progress.Close(record.ID)
		}
	}()

	// Structural liveness: heartbeat for as long as THIS goroutine is alive,
	// independent of stream/tool activity. Previously the heartbeat was driven
	// from inside the inference loop (on each delta/tool), so any synchronous
	// operation that blocks the goroutine without emitting events - a long
	// `exec`, a slow time-to-first-byte, a silent stream - produced no
	// heartbeat and the watchdog force-killed a worker that was actually fine.
	// That was the recurring "worker stalled: no heartbeat" bug. Now the
	// watchdog only fires when the goroutine itself is genuinely dead/wedged.
	{
		interval := time.Duration(o.cfg.Orchestrator.WithDefaults().Completion.HeartbeatIntervalSec) * time.Second / 2
		if interval <= 0 {
			interval = 15 * time.Second
		}
		hb := time.NewTicker(interval)
		defer hb.Stop()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-hb.C:
					o.workers.heartbeat(record.ID, "")
				}
			}
		}()
	}
	record.Status = "in_progress"
	startedAt := time.Now().UTC()
	record.StartedAt = &startedAt
	record.UpdatedAt = startedAt
	_ = o.writeTask(record)
	o.publishTaskUpdate(sessionID, record)

	o.runTaskActor(ctx, snap, sessionID, &record)

	record.UpdatedAt = time.Now().UTC()
	if ctx.Err() != nil && record.Status != "failed" {
		record.Status = "stopped"
	} else if record.Status == "in_progress" {
		// Loop ended without an explicit complete/fail tool call: treat the
		// accumulated result as the outcome for non-executor roles only.
		if record.Error != "" || record.Role == "task-runner" {
			if record.Error == "" {
				record.Error = "executor exited without an explicit terminal tool"
			}
			record.Status = "failed"
		} else {
			record.Status = "done"
		}
	}
	if record.Status == "done" || record.Status == "failed" || record.Status == "stopped" {
		completedAt := record.UpdatedAt
		record.CompletedAt = &completedAt
	}
	o.workers.heartbeat(record.ID, "finalizing")
	if record.Status == "failed" && record.Error != "" {
		o.workerLogError(record.ID, "task failed: "+record.Error)
	}
	_ = o.writeTask(record)
	// Completion trigger: push the terminal/notable state to the widget via the
	// event bus (the `watch` op streams it). This is what lets the chat surface
	// "task done/failed/needs-clarification" without the user polling - the
	// "speak"-style trigger the realtime flow expects.
	o.publishTaskUpdate(sessionID, record)
	// NOTE: plan.md is written ONLY when the planner explicitly calls
	// write_plan (see dispatchTool). We deliberately do
	// NOT synthesize a plan.md from free-form planner text here: a planner that
	// merely answered a question (without producing a real plan) must not leave
	// a fake artifact that can pass explicit plan_task_id validation.
}

// publishTaskUpdate emits durable lifecycle visibility for every background
// sub-agent state. Chat visibility is unconditional: notifyUserOnDone may
// govern desktop notifications later, but it must never hide task certainty.
func (o *Orchestrator) publishTaskUpdate(sessionID string, record taskRecord) {
	// Clarification is mediated before it reaches the UI. A dedicated
	// decision actor first tries to resolve it from shared session/task context;
	// only an unresolved decision is published as an awaiting-clarification
	// task update and spoken into the user conversation.
	if record.Status == "awaiting_clarification" {
		o.resolveClarification(sessionID, record)
		return
	}
	o.publishTaskUpdateDirect(sessionID, record)
}

func (o *Orchestrator) publishTaskUpdateDirect(sessionID string, record taskRecord) {
	ev := taskUpdateEvent(sessionID, record)
	if ev.Kind == "" {
		return
	}
	_ = o.progress.Append(record.ID, ev)
	if o.bus != nil {
		o.bus.Publish(topicFor(bridge.EventTaskUpdate), ev)
	}
	// Event-driven completion: on a terminal transition, also SPEAK the outcome
	// into the conversation so a finish that lands after sapaloq_wait returns is
	// surfaced as a real chat message, not just a card. Idempotent per task id.
	o.speakTaskCompletion(sessionID, record)
}

func (o *Orchestrator) publishTaskActivity(sessionID string, record taskRecord, summary string) {
	record.Status = "in_progress"
	record.UpdatedAt = time.Now().UTC()
	ev := taskUpdateEvent(sessionID, record)
	ev.Summary = summary
	// Orchestrator-only live hint: push to the chat bus so the main transcript
	// can refresh its task card. Do NOT append to the per-task progress JSONL —
	// that stream feeds the sub-agent monitor, which should show
	// thinking/tools/text only (status lives in the pop-up header).
	if o.bus != nil {
		o.bus.Publish(topicFor(bridge.EventTaskUpdate), ev)
	}
}

// RecentTaskUpdates returns the latest durable task states for widget catch-up.
// It makes reconnect/startup independent from whether the UI happened to be
// subscribed at the exact instant an in-memory bus event was published.
func (o *Orchestrator) RecentTaskUpdates(limit int) []bridge.StreamEvent {
	if limit <= 0 {
		limit = 20
	}
	entries, err := os.ReadDir(o.tasksRoot())
	if err != nil {
		return nil
	}
	// Catch-up exists so a widget that connects late still sees work that is
	// IN FLIGHT (pending / in_progress / stopping) or paused waiting on the
	// user (awaiting_clarification). Terminal tasks (done/failed/stopped) do
	// NOT need rehydration: their result is already persisted as an assistant
	// chat bubble (speakTaskCompletion) and shown by the chat-history restore.
	// Re-emitting every historical terminal task made the chat room fill with a
	// verbose status timeline ("planner gagal", "task-runner selesai", ...) on
	// every widget open - the user only wants the live chat + tools.
	live := make([]taskRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, readErr := o.readTask(entry.Name())
		if readErr != nil {
			continue
		}
		switch record.Status {
		case "pending", "in_progress", "stopping", "awaiting_clarification":
			live = append(live, record)
		default:
			// terminal: skip - the outcome lives in chat history already
		}
	}
	sort.Slice(live, func(i, j int) bool {
		return live[i].UpdatedAt.After(live[j].UpdatedAt)
	})
	if len(live) > limit {
		live = live[:limit]
	}
	out := make([]bridge.StreamEvent, 0, len(live))
	for i := len(live) - 1; i >= 0; i-- {
		record := live[i]
		ev := taskUpdateEvent(record.SessionID, record)
		if ev.Kind != "" {
			out = append(out, ev)
		}
	}
	return out
}

func (o *Orchestrator) recoverOrphanedTasks() {
	entries, err := os.ReadDir(o.tasksRoot())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, readErr := o.readTask(entry.Name())
		if readErr != nil {
			continue
		}
		switch record.Status {
		case "pending", "in_progress":
			var turns []chatstore.Turn
			if o.chat != nil {
				turns, _ = o.chat.ActiveTurns(context.Background(), record.ID, true)
			}
			if len(turns) > 0 {
				record.Status = "pending"
				record.Error = ""
				record.UpdatedAt = time.Now().UTC()
				_ = o.writeTask(record)
				o.resumeBackground(o.snapshot(), record.SessionID, record)
				continue
			}
			record.Status = "failed"
			record.Error = "task orphaned by core restart and has no durable actor turns to resume"
			record.UpdatedAt = time.Now().UTC()
			completedAt := record.UpdatedAt
			record.CompletedAt = &completedAt
			_ = o.writeTask(record)
			o.publishTaskUpdate(record.SessionID, record)
		case "stopping":
			record.Status = "stopped"
			record.UpdatedAt = time.Now().UTC()
			completedAt := record.UpdatedAt
			record.CompletedAt = &completedAt
			_ = o.writeTask(record)
			o.publishTaskUpdate(record.SessionID, record)
		}
	}
}

func (o *Orchestrator) migrateLegacyTaskTranscripts() {
	if o == nil || o.chat == nil {
		return
	}
	entries, err := os.ReadDir(o.tasksRoot())
	if err != nil {
		return
	}
	ctx := context.Background()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, readErr := o.readTask(entry.Name())
		if readErr != nil || len(record.Transcript) == 0 {
			continue
		}
		turns, _ := o.chat.ActiveTurns(ctx, record.ID, true)
		if len(turns) > 0 {
			record.Transcript = nil
			_ = o.writeTask(record)
			continue
		}
		if strings.TrimSpace(record.Task) != "" {
			_ = o.chat.AppendTurn(ctx, record.ID, "user", record.Task, estimateTextTokens(record.Task))
		}
		for _, turn := range record.Transcript {
			role := turn.Role
			if role != "user" && role != "assistant" && role != "system" && role != "tool" {
				role = "user"
			}
			_ = o.chat.AppendTurn(ctx, record.ID, role, turn.Content, estimateTextTokens(turn.Content))
		}
		record.Transcript = nil
		_ = o.writeTask(record)
	}
}

func taskUpdateEvent(sessionID string, record taskRecord) bridge.StreamEvent {
	var summary string
	switch record.Status {
	case "pending":
		summary = "Task dijadwalkan dan menunggu worker."
	case "in_progress":
		summary = "Sub-agent sedang mengerjakan task."
	case "done":
		// The card is a STATUS timeline, not a result dump. The full summary is
		// authored by the orchestrator and surfaced as a chat bubble
		// (speakTaskCompletion) - duplicating record.Result here produced two
		// identical, redundant summaries (card + bubble). Keep this terse.
		summary = "Selesai."
	case "failed":
		summary = "Task gagal"
		if e := strings.TrimSpace(record.Error); e != "" {
			summary += ": " + e
		}
	case "awaiting_clarification":
		summary = "Sub-agent butuh keputusan"
		if q := strings.TrimSpace(record.Question); q != "" {
			summary += ": " + q
		}
	case "stopping":
		summary = "Task sedang dihentikan."
	case "stopped":
		summary = "Task dihentikan."
	default:
		return bridge.StreamEvent{}
	}
	if len(summary) > maxTranscriptTurnBytes {
		summary = summary[:maxTranscriptTurnBytes] + "…"
	}
	ev := bridge.NewEvent(bridge.EventTaskUpdate)
	ev.SessionID = sessionID
	ev.TaskID = record.ID
	ev.TaskRole = record.Role
	ev.TaskStatus = record.Status
	ev.Summary = summary
	if !record.UpdatedAt.IsZero() {
		ev.At = record.UpdatedAt
	}
	return ev
}

// validatePlanForAgent makes Plan → Agent handoff explicit and task-scoped.
// Selecting a session's latest plan can attach stale, unrelated work.
func (o *Orchestrator) validatePlanForAgent(sessionID, planTaskID string) error {
	rec, err := o.readTask(planTaskID)
	if err != nil {
		return err
	}
	if rec.Role != "planner" {
		return fmt.Errorf("task %q is not a planner task", planTaskID)
	}
	if rec.Status != "done" {
		return fmt.Errorf("plan %q is %s, not done", planTaskID, rec.Status)
	}
	if sessionID != "" && rec.SessionID != sessionID {
		return fmt.Errorf("plan %q belongs to another session", planTaskID)
	}
	if _, err := os.Stat(filepath.Join(o.taskDir(planTaskID), "plan.md")); err != nil {
		return fmt.Errorf("plan %q has no plan.md", planTaskID)
	}
	return nil
}

// tasksRoot resolves the directory holding per-task records. Production wires
// tasksDir from RuntimeDirs (under state/); when it is unset (e.g. in tests
// that only populate memoryDir) it falls back to the legacy memory/tasks path.
func (o *Orchestrator) tasksRoot() string {
	if o.tasksDir != "" {
		return o.tasksDir
	}
	return filepath.Join(o.memoryDir, "tasks")
}

func (o *Orchestrator) taskDir(id string) string {
	return filepath.Join(o.tasksRoot(), filepath.Base(id))
}

func (o *Orchestrator) writeTask(record taskRecord) error {
	dir := o.taskDir(record.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "status.json"), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	o.notifyTask(record.ID)
	return nil
}

// writeFileAtomic writes data to path atomically: it writes to a temp file in
// the same directory and renames it into place. os.Rename is atomic on the same
// filesystem, so a concurrent reader never observes a truncated/partial file.
// This fixes a race where sapaloq_wait/readTask could read status.json mid-write
// and fail json.Unmarshal with "unexpected end of JSON input".
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if rename succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (o *Orchestrator) readTask(id string) (taskRecord, error) {
	if id == "" || filepath.Base(id) != id {
		return taskRecord{}, fmt.Errorf("valid task_id is required")
	}
	path := filepath.Join(o.taskDir(id), "status.json")
	// Writes are atomic (writeFileAtomic), so a partial read should not happen.
	// Retry defensively against any transient empty/truncated read (e.g. from
	// an external editor) so callers never see "unexpected end of JSON input".
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		raw, err := os.ReadFile(path)
		if err != nil {
			return taskRecord{}, err
		}
		var record taskRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			lastErr = err
			if len(raw) == 0 || err == io.ErrUnexpectedEOF || strings.Contains(err.Error(), "unexpected end of JSON input") {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return taskRecord{}, err
		}
		return record, nil
	}
	return taskRecord{}, lastErr
}

func (o *Orchestrator) notifyTask(id string) {
	o.taskMu.Lock()
	if o.taskSignals == nil {
		o.taskSignals = make(map[string]chan struct{})
	}
	if signal := o.taskSignals[id]; signal != nil {
		close(signal)
	}
	o.taskSignals[id] = make(chan struct{})
	o.taskMu.Unlock()
}

func (o *Orchestrator) taskSignal(id string) <-chan struct{} {
	o.taskMu.Lock()
	defer o.taskMu.Unlock()
	if o.taskSignals == nil {
		o.taskSignals = make(map[string]chan struct{})
	}
	if o.taskSignals[id] == nil {
		o.taskSignals[id] = make(chan struct{})
	}
	return o.taskSignals[id]
}

func (o *Orchestrator) waitForTaskChange(ctx context.Context, taskID string, pollSeconds, maxWaitSeconds int) (taskRecord, bool, error) {
	record, err := o.readTask(taskID)
	if err != nil {
		return taskRecord{}, false, err
	}
	if taskTerminal(record.Status) {
		return record, true, nil
	}
	if pollSeconds < 1 {
		pollSeconds = 2
	}
	if maxWaitSeconds < 1 {
		maxWaitSeconds = 120
	}
	deadline := time.NewTimer(time.Duration(maxWaitSeconds) * time.Second)
	defer deadline.Stop()
	for {
		signal := o.taskSignal(taskID)
		tick := time.NewTimer(time.Duration(pollSeconds) * time.Second)
		select {
		case <-ctx.Done():
			tick.Stop()
			return record, false, ctx.Err()
		case <-deadline.C:
			tick.Stop()
			return record, false, nil
		case <-signal:
			tick.Stop()
		case <-tick.C:
		}
		next, readErr := o.readTask(taskID)
		if readErr != nil {
			return record, false, readErr
		}
		// Only a MEANINGFUL change ends the wait: the task reached a terminal
		// state, or its status transitioned to a different non-terminal state.
		// A bare UpdatedAt bump with the SAME status (e.g. the agent calling
		// sapaloq_update_task_progress) must NOT break the wait - otherwise the
		// orchestrator returns "changed to in_progress", tends to re-wait, and
		// the chat freezes in a wait→progress→wait loop (the "blocking
		// progress" symptom). Such progress is surfaced live via the watch
		// stream as a task card, not by waking the conversational loop.
		if taskTerminal(next.Status) || next.Status != record.Status {
			return next, true, nil
		}
		record = next
	}
}

func taskTerminal(status string) bool {
	// awaiting_clarification is "terminal" for wait purposes: the orchestrator
	// must surface the question to the user rather than keep polling.
	return status == "done" || status == "failed" || status == "stopped" || status == "awaiting_clarification"
}

func statusEvent(sessionID, status string) bridge.StreamEvent {
	return bridge.StreamEvent{Kind: bridge.EventStatus, SessionID: sessionID, Status: status, At: time.Now().UTC()}
}

// waitingEvent is a "waiting" status event that also carries the effective wait
// window so the widget can show a live countdown instead of a static label.
func waitingEvent(sessionID string, seconds int) bridge.StreamEvent {
	ev := statusEvent(sessionID, "waiting")
	ev.WaitSeconds = seconds
	return ev
}

// effectiveWaitSeconds mirrors the deadline in waitForTaskChange so the UI
// countdown matches how long the backend will actually block before giving up.
// The backend deadline is maxWaitSeconds (default 120); the requested seconds
// only controls poll cadence, so the true blocking window is maxWaitSeconds.
func effectiveWaitSeconds(_ /*reqSeconds*/, maxWaitSeconds int) int {
	if maxWaitSeconds < 1 {
		return 120
	}
	return maxWaitSeconds
}
