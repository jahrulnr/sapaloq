package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	terminalTaskRetention = 30 * 24 * time.Hour
	toolJobRetention      = 14 * 24 * time.Hour
	maxTerminalTasks      = 500
	maxToolJobFiles       = 1000
)

type retainedArtifact struct {
	path    string
	name    string
	updated time.Time
	status  string
}

// pruneRuntimeArtifacts bounds transient task/worker/rollout/tool-job state.
// Active and clarification-paused actors are never removed; durable chat and
// memory stores are outside this retention domain.
func (o *Orchestrator) pruneRuntimeArtifacts(now time.Time) {
	if o == nil {
		return
	}
	o.pruneTerminalTasks(now)
	o.pruneToolJobs(now)
}

func (o *Orchestrator) pruneTerminalTasks(now time.Time) {
	entries, err := os.ReadDir(o.tasksRoot())
	if err != nil {
		return
	}
	var terminal []retainedArtifact
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, readErr := o.readTask(entry.Name())
		if readErr != nil || !terminalTaskStatus(record.Status) {
			continue
		}
		updated := record.UpdatedAt
		if updated.IsZero() {
			if info, statErr := entry.Info(); statErr == nil {
				updated = info.ModTime()
			}
		}
		terminal = append(terminal, retainedArtifact{path: o.taskDir(record.ID), name: record.ID, updated: updated, status: record.Status})
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].updated.After(terminal[j].updated) })
	for i, item := range terminal {
		if i < maxTerminalTasks && now.Sub(item.updated) <= terminalTaskRetention {
			continue
		}
		_ = os.RemoveAll(item.path)
		if o.workersDir != "" {
			_ = os.RemoveAll(filepath.Join(o.workersDir, item.name))
		}
		if o.progress != nil && o.progress.inner.Dir != "" {
			_ = os.Remove(filepath.Join(o.progress.inner.Dir, "orch-"+item.name+".jsonl"))
		}
	}
}

func terminalTaskStatus(status string) bool {
	switch status {
	case "done", "failed", "stopped":
		return true
	default:
		return false
	}
}

func (o *Orchestrator) pruneToolJobs(now time.Time) {
	if o.stateDir == "" {
		return
	}
	root := filepath.Join(o.stateDir, "tool-jobs")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var terminal []retainedArtifact
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var meta struct {
			Status      string     `json:"status"`
			CompletedAt *time.Time `json:"completed_at"`
		}
		if json.Unmarshal(raw, &meta) != nil || !terminalJobStatus(meta.Status) {
			continue
		}
		updated := time.Time{}
		if meta.CompletedAt != nil {
			updated = *meta.CompletedAt
		} else if info, statErr := entry.Info(); statErr == nil {
			updated = info.ModTime()
		}
		terminal = append(terminal, retainedArtifact{path: path, name: entry.Name(), updated: updated, status: meta.Status})
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].updated.After(terminal[j].updated) })
	for i, item := range terminal {
		if i < maxToolJobFiles && now.Sub(item.updated) <= toolJobRetention {
			continue
		}
		_ = os.Remove(item.path)
	}
}

func terminalJobStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}
