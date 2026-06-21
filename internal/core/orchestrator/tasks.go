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
	// Transcript is a minimal message log (role+content) of the sub-agent's
	// conversation so far. It lets a task paused on awaiting_clarification be
	// resumed by reconstruction (the in-memory messages slice is lost when the
	// goroutine returns). Bounded by maxTurns; each entry is content-capped.
	Transcript []taskTurn `json:"transcript,omitempty"`
	// Answer is the user's clarification answer, set transiently on resume and
	// consumed by buildSubAgentMessages as the resume nudge.
	Answer string `json:"answer,omitempty"`
	// Node is the execution node this task was routed to (observability).
	// "local-default" (or empty) means in-proc execution.
	Node      string    `json:"node,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// appendTranscript records a role/content turn on the record, truncating the
// content to maxTranscriptTurnBytes. Empty content is skipped.
func (r *taskRecord) appendTranscript(role, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	if len(content) > maxTranscriptTurnBytes {
		content = content[:maxTranscriptTurnBytes] + "…[truncated]"
	}
	r.Transcript = append(r.Transcript, taskTurn{Role: role, Content: content})
}

type askToolResult struct {
	text    string
	handled bool
	stop    bool
}

func (o *Orchestrator) handleAskTool(ctx context.Context, snap providerSnapshot, out chan<- bridge.StreamEvent, sessionID, fallbackTask string, call parse.ToolCall) askToolResult {
	var args struct {
		Task       string `json:"task"`
		TaskID     string `json:"task_id"`
		PlanTaskID string `json:"plan_task_id"`
		Seconds    int    `json:"seconds"`
		Reason     string `json:"reason"`
		Scope      string `json:"scope"`
		Answer     string `json:"answer"`
	}
	_ = json.Unmarshal(call.Arguments, &args)
	o.auditTool(sessionID, "ask", call)
	switch call.Name {
	case "sapaloq_spawn_plan":
		task := strings.TrimSpace(args.Task)
		if task == "" {
			task = fallbackTask
		}
		task = carryImageAttachments(task, fallbackTask)
		id, err := o.spawnBackground(snap, sessionID, "planner", task, "")
		if err != nil {
			return askToolResult{text: "Failed to start planner: " + err.Error(), handled: true}
		}
		return askToolResult{text: fmt.Sprintf("Planner started in background (`%s`).", id), handled: true}
	case "sapaloq_spawn_agent":
		task := strings.TrimSpace(args.Task)
		if task == "" {
			task = fallbackTask
		}
		task = carryImageAttachments(task, fallbackTask)
		planTaskID := strings.TrimSpace(args.PlanTaskID)
		if planTaskID != "" {
			if err := o.validatePlanForAgent(sessionID, planTaskID); err != nil {
				return askToolResult{text: "Cannot use plan: " + err.Error(), handled: true}
			}
		}
		id, err := o.spawnBackground(snap, sessionID, "task-runner", task, planTaskID)
		if err != nil {
			return askToolResult{text: "Failed to start agent: " + err.Error(), handled: true}
		}
		msg := fmt.Sprintf("Agent started in background (`%s`).", id)
		if planTaskID != "" {
			msg += fmt.Sprintf(" Using plan from `%s`.", planTaskID)
		}
		return askToolResult{text: msg, handled: true}
	case "sapaloq_spawn_scribe":
		task := strings.TrimSpace(args.Task)
		if task == "" {
			task = fallbackTask
		}
		id, err := o.spawnBackground(snap, sessionID, "scribe", task, "")
		if err != nil {
			return askToolResult{text: "Failed to start scribe: " + err.Error(), handled: true}
		}
		return askToolResult{text: fmt.Sprintf("Scribe started in background (`%s`).", id), handled: true}
	case "sapaloq_get_task_status":
		taskID := strings.TrimSpace(args.TaskID)
		if taskID == "" {
			taskID = o.latestTaskID()
		}
		record, err := o.readTask(taskID)
		if err != nil {
			return askToolResult{text: "Task status unavailable: " + err.Error(), handled: true}
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
		return askToolResult{text: response, handled: true}
	case "sapaloq_wait":
		taskID := strings.TrimSpace(args.TaskID)
		if taskID == "" {
			taskID = o.latestTaskID()
		}
		waitSecs := effectiveWaitSeconds(args.Seconds, snap.cfg.Orchestrator.Continuation.MaxWaitSeconds)
		o.emit(ctx, out, waitingEvent(sessionID, waitSecs))
		record, changed, err := o.waitForTaskChange(ctx, taskID, args.Seconds, snap.cfg.Orchestrator.Continuation.MaxWaitSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return askToolResult{text: "Wait cancelled.", handled: true, stop: true}
			}
			return askToolResult{text: "Wait failed: " + err.Error(), handled: true}
		}
		o.emit(ctx, out, statusEvent(sessionID, "working"))
		if !changed {
			// IMPORTANT: do NOT imply you will keep watching. This generation is
			// about to end; the task keeps running in the background and its
			// completion is delivered asynchronously (the orchestrator speaks it
			// into chat on the terminal transition). Tell the user it will be
			// surfaced automatically instead of promising to "wait a bit more".
			return askToolResult{text: fmt.Sprintf(
				"Task `%s` masih %s setelah jendela tunggu. Tidak perlu menunggu — aku akan otomatis mengabari di chat begitu task selesai/gagal/butuh keputusan (kamu juga bisa cek `sapaloq_get_task_status`).",
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
		return askToolResult{text: response, handled: true}
	case "sapaloq_answer_clarification":
		answer := strings.TrimSpace(args.Answer)
		if answer == "" {
			return askToolResult{text: "Error: answer is required.", handled: true}
		}
		taskID := strings.TrimSpace(args.TaskID)
		if taskID == "" {
			taskID = o.latestAwaitingTaskID(sessionID)
		}
		if taskID == "" {
			return askToolResult{text: "No task is awaiting clarification.", handled: true}
		}
		record, err := o.readTask(taskID)
		if err != nil {
			return askToolResult{text: "Task unavailable: " + err.Error(), handled: true}
		}
		if record.Status != "awaiting_clarification" {
			return askToolResult{text: fmt.Sprintf("Task `%s` is %s, not awaiting clarification; cannot answer.", record.ID, record.Status), handled: true}
		}
		record.Answer = answer
		record.Question = ""
		record.Status = "in_progress"
		record.UpdatedAt = time.Now().UTC()
		if err := o.writeTask(record); err != nil {
			return askToolResult{text: "Failed to persist answer: " + err.Error(), handled: true}
		}
		o.resumeBackground(snap, sessionID, record)
		return askToolResult{text: fmt.Sprintf("Answer delivered; task `%s` resumed in the background.", record.ID), handled: true}
	case "sapaloq_stop":
		reason := strings.TrimSpace(args.Reason)
		if reason == "" {
			reason = "model requested the continuation to stop"
		}
		scope := strings.TrimSpace(args.Scope)
		if scope == "" || scope == "generation" {
			return askToolResult{text: "Stopped: " + reason, handled: true, stop: true}
		}
		if scope == "task" {
			stopped := o.stopTask(args.TaskID)
			message := "no active task"
			if stopped {
				message = "task stopped"
			}
			return askToolResult{text: message + ": " + reason, handled: true}
		}
		if scope == "all" {
			stopped := 0
			for _, id := range o.tasksForSession(sessionID) {
				if o.stopTask(id) {
					stopped++
				}
			}
			return askToolResult{text: fmt.Sprintf("generation and %d task(s) stopped: %s", stopped, reason), handled: true, stop: true}
		}
		return askToolResult{text: "Invalid stop scope: " + scope, handled: true}
	case "desktop_notify":
		return askToolResult{text: o.toolDesktopNotify(ctx, parseToolArgs(call.Arguments)), handled: true}
	case "desktop_dnd_status":
		return askToolResult{text: o.toolDesktopDNDStatus(ctx), handled: true}
	default:
		// Read-only assessment + web tools shared across all modes so Ask is
		// no longer blind: it can read, search, and research before delegating.
		if text, ok := runSharedTool(ctx, call); ok {
			return askToolResult{text: text, handled: true}
		}
		return askToolResult{}
	}
}

func (o *Orchestrator) latestTaskID() string {
	entries, err := os.ReadDir(filepath.Join(o.memoryDir, "tasks"))
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
	now := time.Now().UTC()
	id := fmt.Sprintf("task-%d", now.UnixNano())
	// Route the spawn to an execution node. With only local-default configured
	// this resolves to in-proc execution (unchanged behavior). The chosen node
	// name is recorded for observability.
	node := o.pickNode(context.Background(), role, "")
	record := taskRecord{ID: id, SessionID: sessionID, Role: role, Status: "pending", Task: task, PlanTaskID: planTaskID, Node: node.Name, CreatedAt: now, UpdatedAt: now}
	if err := o.writeTask(record); err != nil {
		return "", err
	}
	o.publishTaskUpdate(sessionID, record)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	o.taskMu.Lock()
	if o.taskCancels == nil {
		o.taskCancels = make(map[string]context.CancelFunc)
	}
	if o.sessionTasks == nil {
		o.sessionTasks = make(map[string]map[string]struct{})
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
	entries, err := os.ReadDir(filepath.Join(o.memoryDir, "tasks"))
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
	}()
	record.Status = "in_progress"
	record.UpdatedAt = time.Now().UTC()
	_ = o.writeTask(record)
	o.publishTaskUpdate(sessionID, record)

	o.runSubAgentLoop(ctx, snap, sessionID, &record)

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
	o.workers.heartbeat(record.ID, "finalizing")
	if record.Status == "failed" && record.Error != "" {
		o.workerLogError(record.ID, "task failed: "+record.Error)
	}
	_ = o.writeTask(record)
	// Completion trigger: push the terminal/notable state to the widget via the
	// event bus (the `watch` op streams it). This is what lets the chat surface
	// "task done/failed/needs-clarification" without the user polling — the
	// "speak"-style trigger the realtime flow expects.
	o.publishTaskUpdate(sessionID, record)
	// NOTE: plan.md is written ONLY when the planner explicitly calls
	// sapaloq_write_plan_markdown (see handleSubAgentTool). We deliberately do
	// NOT synthesize a plan.md from free-form planner text here: a planner that
	// merely answered a question (without producing a real plan) must not leave
	// a fake artifact that can pass explicit plan_task_id validation.
}

// publishTaskUpdate emits durable lifecycle visibility for every background
// sub-agent state. Chat visibility is unconditional: notifyUserOnDone may
// govern desktop notifications later, but it must never hide task certainty.
func (o *Orchestrator) publishTaskUpdate(sessionID string, record taskRecord) {
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
	_ = o.progress.Append(record.ID, ev)
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
	entries, err := os.ReadDir(filepath.Join(o.memoryDir, "tasks"))
	if err != nil {
		return nil
	}
	records := make([]taskRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, readErr := o.readTask(entry.Name())
		if readErr == nil {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].UpdatedAt.After(records[j].UpdatedAt)
	})
	if len(records) > limit {
		records = records[:limit]
	}
	out := make([]bridge.StreamEvent, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		ev := taskUpdateEvent(record.SessionID, record)
		if ev.Kind != "" {
			out = append(out, ev)
		}
	}
	return out
}

func (o *Orchestrator) recoverOrphanedTasks() {
	entries, err := os.ReadDir(filepath.Join(o.memoryDir, "tasks"))
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
		case "pending", "in_progress", "stopping":
			record.Status = "failed"
			record.Error = "task orphaned by core restart; no worker is attached"
			record.UpdatedAt = time.Now().UTC()
			_ = o.writeTask(record)
			o.publishTaskUpdate(record.SessionID, record)
		}
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
		summary = strings.TrimSpace(record.Result)
		if summary == "" {
			summary = "Task selesai."
		}
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

func (o *Orchestrator) taskDir(id string) string {
	return filepath.Join(o.memoryDir, "tasks", filepath.Base(id))
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
		if next.UpdatedAt.After(record.UpdatedAt) || next.Status != record.Status {
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
