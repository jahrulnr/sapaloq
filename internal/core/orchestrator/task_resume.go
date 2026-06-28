package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ResumeNudge is injected once when a failed/stopped task resumes so the actor
// continues from persisted turns instead of restarting exploration.
func buildResumeNudge(priorStatus, priorError string) string {
	var b strings.Builder
	b.WriteString("Resume this task from the conversation below. Do not restart exploration or redo completed work from scratch.")
	if priorError != "" {
		b.WriteString("\nPrior failure (")
		b.WriteString(priorStatus)
		b.WriteString("): ")
		b.WriteString(priorError)
	} else if priorStatus == "stopped" {
		b.WriteString("\nThe task was stopped before completion; continue where you left off.")
	}
	b.WriteString("\nProceed with the remaining work.")
	return b.String()
}

func (o *Orchestrator) taskHasDurableTurns(taskID string) bool {
	if o == nil || o.chat == nil {
		return false
	}
	turns, err := o.chat.ActiveTurns(context.Background(), taskID, true)
	return err == nil && len(turns) > 0
}

func (o *Orchestrator) isTaskRunning(taskID string) bool {
	if o == nil {
		return false
	}
	o.taskMu.Lock()
	_, running := o.taskCancels[taskID]
	o.taskMu.Unlock()
	return running
}

func (o *Orchestrator) taskResumable(record taskRecord) bool {
	if record.Status != "failed" && record.Status != "stopped" {
		return false
	}
	if !o.taskHasDurableTurns(record.ID) {
		return false
	}
	return !o.isTaskRunning(record.ID)
}

func isTransientTaskFailure(errMsg string) bool {
	if strings.TrimSpace(errMsg) == "" {
		return false
	}
	lower := strings.ToLower(errMsg)
	for _, needle := range []string{
		"provider", "connection", "timeout", "orphaned", "stalled",
		"sse", "unavailable", "broken pipe", "eof", "429", "503", "502",
		"reset by peer", "i/o timeout", "core restart",
		"empty response", "returned no data",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func (o *Orchestrator) latestResumableTaskID(sessionID, role string) string {
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
		if readErr != nil || !o.taskResumable(rec) {
			continue
		}
		if sessionID != "" && rec.SessionID != sessionID {
			continue
		}
		if role != "" && rec.Role != role {
			continue
		}
		if rec.UpdatedAt.After(bestTime) {
			bestTime = rec.UpdatedAt
			bestID = rec.ID
		}
	}
	return bestID
}

func (o *Orchestrator) resumeTask(snap providerSnapshot, sessionID, taskID string) (string, error) {
	record, err := o.readTask(taskID)
	if err != nil {
		return "", err
	}
	if sessionID != "" && record.SessionID != sessionID {
		return "", fmt.Errorf("task `%s` belongs to another session", taskID)
	}
	if record.Status == "awaiting_clarification" {
		return "", fmt.Errorf("task `%s` awaits clarification; use sapaloq_answer_clarification", taskID)
	}
	if o.isTaskRunning(record.ID) {
		return "", fmt.Errorf("task `%s` is already running", taskID)
	}
	if !o.taskHasDurableTurns(record.ID) {
		return "", fmt.Errorf("task `%s` has no persisted turns to resume", taskID)
	}
	if record.Status != "failed" && record.Status != "stopped" {
		return "", fmt.Errorf("task `%s` is %s and cannot be resumed", taskID, record.Status)
	}
	priorError := record.Error
	priorStatus := record.Status
	record.Error = ""
	record.Question = ""
	record.Status = "pending"
	record.UpdatedAt = time.Now().UTC()
	record.CompletedAt = nil
	record.ResumeNudge = buildResumeNudge(priorStatus, priorError)
	if err := o.writeTask(record); err != nil {
		return "", err
	}
	o.resumeBackground(snap, record.SessionID, record)
	return record.ID, nil
}

// ResumeTask re-enters a failed or stopped background task from persisted turns.
func (o *Orchestrator) ResumeTask(ctx context.Context, sessionID, taskID string) (string, error) {
	_ = ctx
	o.reloadConfigIfChanged(context.Background())
	return o.resumeTask(o.snapshot(), sessionID, taskID)
}

func (o *Orchestrator) listTasksForSession(sessionID string) []taskRecord {
	entries, err := os.ReadDir(o.tasksRoot())
	if err != nil {
		return nil
	}
	var out []taskRecord
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rec, readErr := o.readTask(entry.Name())
		if readErr != nil || rec.SessionID != sessionID {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func (o *Orchestrator) purgeSessionTasks(sessionID string) {
	for _, record := range o.listTasksForSession(sessionID) {
		o.purgeTaskArtifacts(record.ID)
	}
	o.taskMu.Lock()
	delete(o.sessionTasks, sessionID)
	o.taskMu.Unlock()
}

func (o *Orchestrator) purgeTaskArtifacts(taskID string) {
	if taskID == "" {
		return
	}
	_ = os.RemoveAll(o.taskDir(taskID))
	if o.progress != nil {
		o.progress.Close(taskID)
		if dir := o.progressDir(); dir != "" {
			_ = os.Remove(filepath.Join(dir, "orch-"+taskID+".jsonl"))
		}
	}
	_ = os.Remove(o.workspaceStatePath(taskID))
	if inbox := o.actorInboxRoot(taskID); inbox != "" {
		_ = os.RemoveAll(inbox)
	}
}
