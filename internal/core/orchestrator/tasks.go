package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

var askTools = []string{
	"sapaloq_spawn_plan",
	"sapaloq_spawn_agent",
	"sapaloq_get_task_status",
	"sapaloq_wait",
	"sapaloq_stop",
}

type taskRecord struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id,omitempty"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	Task      string    `json:"task"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type askToolResult struct {
	text    string
	handled bool
	stop    bool
}

func (o *Orchestrator) handleAskTool(ctx context.Context, snap providerSnapshot, out chan<- bridge.StreamEvent, sessionID, fallbackTask string, call parse.ToolCall) askToolResult {
	var args struct {
		Task    string `json:"task"`
		TaskID  string `json:"task_id"`
		Seconds int    `json:"seconds"`
		Reason  string `json:"reason"`
		Scope   string `json:"scope"`
	}
	_ = json.Unmarshal(call.Arguments, &args)
	switch call.Name {
	case "sapaloq_spawn_plan":
		task := strings.TrimSpace(args.Task)
		if task == "" {
			task = fallbackTask
		}
		task = carryImageAttachments(task, fallbackTask)
		id, err := o.spawnBackground(snap, sessionID, "planner", task)
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
		id, err := o.spawnBackground(snap, sessionID, "task-runner", task)
		if err != nil {
			return askToolResult{text: "Failed to start agent: " + err.Error(), handled: true}
		}
		return askToolResult{text: fmt.Sprintf("Agent started in background (`%s`).", id), handled: true}
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
		o.emit(ctx, out, statusEvent(sessionID, "waiting"))
		record, changed, err := o.waitForTaskChange(ctx, taskID, args.Seconds, snap.cfg.Orchestrator.Continuation.MaxWaitSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return askToolResult{text: "Wait cancelled.", handled: true, stop: true}
			}
			return askToolResult{text: "Wait failed: " + err.Error(), handled: true}
		}
		o.emit(ctx, out, statusEvent(sessionID, "working"))
		if !changed {
			return askToolResult{text: fmt.Sprintf("Task `%s` is still %s after the backend wait window.", record.ID, record.Status), handled: true}
		}
		response := fmt.Sprintf("Task `%s` changed to **%s**.", record.ID, record.Status)
		if record.Result != "" {
			response += "\n\n" + record.Result
		}
		if record.Error != "" {
			response += "\n\nError: " + record.Error
		}
		return askToolResult{text: response, handled: true}
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
	default:
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

func (o *Orchestrator) spawnBackground(snap providerSnapshot, sessionID, role, task string) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("task is required")
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("task-%d", now.UnixNano())
	record := taskRecord{ID: id, SessionID: sessionID, Role: role, Status: "pending", Task: task, CreatedAt: now, UpdatedAt: now}
	if err := o.writeTask(record); err != nil {
		return "", err
	}
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

func (o *Orchestrator) runBackgroundTask(ctx context.Context, cancel context.CancelFunc, snap providerSnapshot, sessionID string, record taskRecord) {
	defer cancel()
	defer func() {
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

	system := "You are a background SapaLOQ task agent. Return a concise final result."
	if record.Role == "planner" {
		system = "You are SapaLOQ's read-only planner. Produce a concrete Markdown plan with goal, steps, risks, and acceptance criteria. Do not claim implementation."
	}
	cleanMessages, images := extractImages([]bridge.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: record.Task},
	})
	stream, err := snap.br.Complete(ctx, bridge.Request{
		SessionID: sessionID + ":" + record.ID,
		Model:     snap.entry.Model,
		Messages:  cleanMessages,
		Images:    images,
	})
	if err != nil {
		if ctx.Err() != nil {
			record.Status = "stopped"
		} else {
			record.Status = "failed"
			record.Error = err.Error()
		}
		record.UpdatedAt = time.Now().UTC()
		_ = o.writeTask(record)
		return
	}
	var result strings.Builder
	for ev := range stream {
		_ = o.progress.Append(record.ID, ev)
		if ev.Kind == bridge.EventResponseDelta {
			result.WriteString(ev.Delta)
		}
		if ev.Kind == bridge.EventError {
			record.Error = ev.Error
		}
	}
	record.Result = strings.TrimSpace(result.String())
	record.UpdatedAt = time.Now().UTC()
	if ctx.Err() != nil {
		record.Status = "stopped"
	} else if record.Error != "" {
		record.Status = "failed"
	} else {
		record.Status = "done"
	}
	_ = o.writeTask(record)
	if record.Role == "planner" && record.Result != "" {
		_ = os.WriteFile(filepath.Join(o.taskDir(record.ID), "plan.md"), []byte(record.Result+"\n"), 0o600)
	}
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
	if err := os.WriteFile(filepath.Join(dir, "status.json"), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	o.notifyTask(record.ID)
	return nil
}

func (o *Orchestrator) readTask(id string) (taskRecord, error) {
	if id == "" || filepath.Base(id) != id {
		return taskRecord{}, fmt.Errorf("valid task_id is required")
	}
	raw, err := os.ReadFile(filepath.Join(o.taskDir(id), "status.json"))
	if err != nil {
		return taskRecord{}, err
	}
	var record taskRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return taskRecord{}, err
	}
	return record, nil
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
	return status == "done" || status == "failed" || status == "stopped"
}

func statusEvent(sessionID, status string) bridge.StreamEvent {
	return bridge.StreamEvent{Kind: bridge.EventStatus, SessionID: sessionID, Status: status, At: time.Now().UTC()}
}
