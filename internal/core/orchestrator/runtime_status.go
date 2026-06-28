package orchestrator

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
)

type ActorRuntimeStatus struct {
	ID            string    `json:"id"`
	Role          string    `json:"role"`
	Status        string    `json:"status"`
	Phase         string    `json:"phase"`
	Workspace     string    `json:"workspace"`
	StartedAt     time.Time `json:"started_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

type RuntimeStatus struct {
	Provider      string               `json:"provider"`
	Model         string               `json:"model"`
	Driver        string               `json:"driver"`
	Reasoning     string               `json:"reasoning,omitempty"`
	ConfigPath    string               `json:"config_path"`
	DataPath      string               `json:"data_path"`
	MemoryPath    string               `json:"memory_path"`
	StatePath     string               `json:"state_path"`
	WorkspacePath    string               `json:"workspace_path"`
	SessionID        string               `json:"session_id,omitempty"`
	SessionWorkspace string               `json:"session_workspace,omitempty"`
	Actors           []ActorRuntimeStatus `json:"actors"`
}

func (o *Orchestrator) RuntimeStatus() RuntimeStatus {
	snap := o.snapshot()
	dirs := config.RuntimeDirs(snap.cfg)
	status := RuntimeStatus{
		Provider:      snap.entry.Key,
		Model:         snap.entry.Model,
		Driver:        snap.entry.Driver,
		Reasoning:     snap.entry.ReasoningEffort,
		ConfigPath:    o.cfgPath,
		DataPath:      dirs.DataDir,
		MemoryPath:    dirs.MemoryDir,
		StatePath:     dirs.StateDir,
		WorkspacePath: dirs.WorkspaceDir,
	}
	if o.chat != nil {
		if sessionID, err := o.ActiveSession(context.Background()); err == nil && strings.TrimSpace(sessionID) != "" {
			status.SessionID = sessionID
			status.SessionWorkspace = o.actorCWD(sessionID)
		}
	}
	if o.workers == nil {
		return status
	}
	for _, worker := range o.workers.snapshot() {
		status.Actors = append(status.Actors, ActorRuntimeStatus{
			ID:            worker.ID,
			Role:          worker.Role,
			Status:        worker.Status,
			Phase:         worker.Phase,
			Workspace:     o.actorCWD(worker.ID),
			StartedAt:     worker.StartedAt,
			LastHeartbeat: worker.LastHeartbeat,
		})
	}
	sort.Slice(status.Actors, func(i, j int) bool {
		return status.Actors[i].StartedAt.Before(status.Actors[j].StartedAt)
	})
	return status
}
