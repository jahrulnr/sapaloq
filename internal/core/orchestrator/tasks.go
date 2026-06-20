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
}

type taskRecord struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	Task      string    `json:"task"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (o *Orchestrator) handleAskTool(ctx context.Context, snap providerSnapshot, sessionID, fallbackTask string, call parse.ToolCall) (string, bool) {
	var args struct {
		Task   string `json:"task"`
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(call.Arguments, &args)
	switch call.Name {
	case "sapaloq_spawn_plan":
		task := strings.TrimSpace(args.Task)
		if task == "" {
			task = fallbackTask
		}
		id, err := o.spawnBackground(snap, sessionID, "planner", task)
		if err != nil {
			return "\nFailed to start planner: " + err.Error(), true
		}
		return fmt.Sprintf("\nPlanner started in background (`%s`).", id), true
	case "sapaloq_spawn_agent":
		task := strings.TrimSpace(args.Task)
		if task == "" {
			task = fallbackTask
		}
		id, err := o.spawnBackground(snap, sessionID, "task-runner", task)
		if err != nil {
			return "\nFailed to start agent: " + err.Error(), true
		}
		return fmt.Sprintf("\nAgent started in background (`%s`).", id), true
	case "sapaloq_get_task_status":
		record, err := o.readTask(strings.TrimSpace(args.TaskID))
		if err != nil {
			return "\nTask status unavailable: " + err.Error(), true
		}
		response := fmt.Sprintf("\nTask `%s` is **%s**.", record.ID, record.Status)
		if record.Result != "" {
			response += "\n\n" + record.Result
		}
		if record.Error != "" {
			response += "\n\nError: " + record.Error
		}
		return response, true
	default:
		return "", false
	}
}

func (o *Orchestrator) spawnBackground(snap providerSnapshot, sessionID, role, task string) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("task is required")
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("task-%d", now.UnixNano())
	record := taskRecord{ID: id, Role: role, Status: "pending", Task: task, CreatedAt: now, UpdatedAt: now}
	if err := o.writeTask(record); err != nil {
		return "", err
	}
	go o.runBackgroundTask(snap, sessionID, record)
	return id, nil
}

func (o *Orchestrator) runBackgroundTask(snap providerSnapshot, sessionID string, record taskRecord) {
	record.Status = "in_progress"
	record.UpdatedAt = time.Now().UTC()
	_ = o.writeTask(record)

	system := "You are a background SapaLOQ task agent. Return a concise final result."
	if record.Role == "planner" {
		system = "You are SapaLOQ's read-only planner. Produce a concrete Markdown plan with goal, steps, risks, and acceptance criteria. Do not claim implementation."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	stream, err := snap.br.Complete(ctx, bridge.Request{
		SessionID: sessionID + ":" + record.ID,
		Model:     snap.entry.Model,
		Messages: []bridge.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: record.Task},
		},
	})
	if err != nil {
		record.Status = "failed"
		record.Error = err.Error()
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
	if record.Error != "" {
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
	return os.WriteFile(filepath.Join(dir, "status.json"), append(raw, '\n'), 0o600)
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
