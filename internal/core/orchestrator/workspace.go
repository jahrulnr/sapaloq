package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type actorRunIDKey struct{}

func withActorRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, actorRunIDKey{}, runID)
}

func actorRunID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	runID, _ := ctx.Value(actorRunIDKey{}).(string)
	return runID
}

type workspaceState struct {
	CWD string `json:"cwd"`
}

func (o *Orchestrator) workspacesDir() string {
	root := o.stateDir
	if root == "" {
		root = filepath.Join(configDataRootFallback(), "state")
	}
	return filepath.Join(root, "workspaces")
}

func (o *Orchestrator) defaultWorkspace() string {
	if strings.TrimSpace(o.workspaceDir) != "" {
		return o.workspaceDir
	}
	return filepath.Join(configDataRootFallback(), "workspace")
}

func isChatSessionID(runID string) bool {
	return strings.HasPrefix(runID, "chat-")
}

func configDataRootFallback() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "SapaLOQ")
}

func (o *Orchestrator) workspaceStatePath(runID string) string {
	return filepath.Join(o.workspacesDir(), safeActorID(runID)+".json")
}

func (o *Orchestrator) lastWorkspacePath() string {
	return filepath.Join(o.workspacesDir(), "_last.json")
}

func (o *Orchestrator) readLastWorkspace() string {
	defaultDir := filepath.Clean(o.defaultWorkspace())
	raw, err := os.ReadFile(o.lastWorkspacePath())
	if err != nil {
		return ""
	}
	var state workspaceState
	if json.Unmarshal(raw, &state) != nil {
		return ""
	}
	cwd := filepath.Clean(strings.TrimSpace(state.CWD))
	if cwd == "" || cwd == defaultDir {
		return ""
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return ""
	}
	return cwd
}

func (o *Orchestrator) writeLastWorkspace(cwd string) {
	defaultDir := filepath.Clean(o.defaultWorkspace())
	cleaned := filepath.Clean(strings.TrimSpace(cwd))
	if cleaned == "" || cleaned == defaultDir {
		return
	}
	if info, err := os.Stat(cleaned); err != nil || !info.IsDir() {
		return
	}
	if os.MkdirAll(o.workspacesDir(), 0o700) != nil {
		return
	}
	raw, err := json.MarshalIndent(workspaceState{CWD: cleaned}, "", "  ")
	if err != nil {
		return
	}
	_ = writeFileAtomic(o.lastWorkspacePath(), raw, 0o600)
}

// actorCWD returns the persisted cwd for an actor/session id, or the install
// default when no chat-specific file exists. Chat rooms without a file inherit
// _last.json (explicit WORKSPACE picker history) but never another room's file.
func (o *Orchestrator) actorCWD(runID string) string {
	defaultDir := filepath.Clean(o.defaultWorkspace())
	if runID == "" {
		return defaultDir
	}
	raw, err := os.ReadFile(o.workspaceStatePath(runID))
	if err != nil {
		if isChatSessionID(runID) {
			if last := o.readLastWorkspace(); last != "" {
				return last
			}
		}
		return defaultDir
	}
	var state workspaceState
	if json.Unmarshal(raw, &state) != nil || strings.TrimSpace(state.CWD) == "" {
		return defaultDir
	}
	cwd := filepath.Clean(state.CWD)
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return defaultDir
	}
	// Legacy chat files that only recorded the install default are treated as unset.
	if isChatSessionID(runID) && cwd == defaultDir {
		if last := o.readLastWorkspace(); last != "" {
			return last
		}
		return defaultDir
	}
	return cwd
}

// SessionWorkspace returns the resolved cwd for a chat/task actor (for IPC/widget).
func (o *Orchestrator) SessionWorkspace(sessionID string) string {
	return o.actorCWD(sessionID)
}

// persistChatSessionWorkspace records an explicit WORKSPACE picker choice for one
// chat room. The install default is represented by absence of a file.
// sapaloq:boundary ipc→orchestrator — workspace_set only; agent cwd does not auto-persist chat rooms.
func (o *Orchestrator) persistChatSessionWorkspace(sessionID, cwd string) {
	if !isChatSessionID(sessionID) || strings.TrimSpace(cwd) == "" {
		return
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return
	}
	cleaned := filepath.Clean(cwd)
	defaultDir := filepath.Clean(o.defaultWorkspace())
	path := o.workspaceStatePath(sessionID)
	if cleaned == defaultDir {
		_ = os.Remove(path)
		return
	}
	raw, err := json.MarshalIndent(workspaceState{CWD: cleaned}, "", "  ")
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = writeFileAtomic(path, raw, 0o600)
	o.writeLastWorkspace(cleaned)
}

// persistActorCWD updates cwd for background actors (task-*, agent runs). Foreground
// chat sessions use persistChatSessionWorkspace from the WORKSPACE picker only.
func (o *Orchestrator) persistActorCWD(runID, cwd string) {
	if runID == "" || strings.TrimSpace(cwd) == "" || isChatSessionID(runID) {
		return
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return
	}
	raw, err := json.MarshalIndent(workspaceState{CWD: filepath.Clean(cwd)}, "", "  ")
	if err != nil {
		return
	}
	path := o.workspaceStatePath(runID)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = writeFileAtomic(path, raw, 0o600)
}

func (o *Orchestrator) resolveActorArgs(ctx context.Context, args toolArgs) toolArgs {
	if o == nil || o.workspaceDir == "" && o.stateDir == "" && actorRunID(ctx) == "" {
		return args
	}
	base := o.actorCWD(actorRunID(ctx))
	if strings.TrimSpace(args.Cwd) == "" {
		args.Cwd = base
	} else if !filepath.IsAbs(configExpandHome(args.Cwd)) {
		args.Cwd = filepath.Join(base, configExpandHome(args.Cwd))
	} else {
		args.Cwd = configExpandHome(args.Cwd)
	}
	if strings.TrimSpace(args.Path) != "" && !filepath.IsAbs(configExpandHome(args.Path)) {
		args.Path = filepath.Join(base, configExpandHome(args.Path))
	}
	return args
}

func configExpandHome(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
