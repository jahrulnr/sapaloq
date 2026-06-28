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

const lastWorkspaceFile = "_last.json"

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

func (o *Orchestrator) lastWorkspace() string {
	raw, err := os.ReadFile(filepath.Join(o.workspacesDir(), lastWorkspaceFile))
	if err != nil {
		return ""
	}
	var state workspaceState
	if json.Unmarshal(raw, &state) != nil || strings.TrimSpace(state.CWD) == "" {
		return ""
	}
	if info, err := os.Stat(state.CWD); err != nil || !info.IsDir() {
		return ""
	}
	return state.CWD
}

func (o *Orchestrator) persistLastWorkspace(cwd string) {
	if strings.TrimSpace(cwd) == "" {
		return
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return
	}
	raw, err := json.MarshalIndent(workspaceState{CWD: filepath.Clean(cwd)}, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(o.workspacesDir(), lastWorkspaceFile)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = writeFileAtomic(path, raw, 0o600)
}

func (o *Orchestrator) fallbackWorkspace(defaultDir, runID string) string {
	if isChatSessionID(runID) {
		if last := o.lastWorkspace(); last != "" {
			return last
		}
	}
	return defaultDir
}

// inheritWorkspace copies the persisted cwd from one actor/session to another.
// Used when /reset or "new chat" mints a fresh session id.
func (o *Orchestrator) inheritWorkspace(fromID, toID string) {
	if fromID == "" || toID == "" || fromID == toID {
		return
	}
	cwd := o.actorCWD(fromID)
	o.persistActorCWD(toID, cwd)
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

func (o *Orchestrator) actorCWD(runID string) string {
	defaultDir := o.defaultWorkspace()
	if runID == "" {
		return o.fallbackWorkspace(defaultDir, runID)
	}
	raw, err := os.ReadFile(o.workspaceStatePath(runID))
	if err != nil {
		return o.fallbackWorkspace(defaultDir, runID)
	}
	var state workspaceState
	if json.Unmarshal(raw, &state) != nil || strings.TrimSpace(state.CWD) == "" {
		return o.fallbackWorkspace(defaultDir, runID)
	}
	if info, err := os.Stat(state.CWD); err != nil || !info.IsDir() {
		return o.fallbackWorkspace(defaultDir, runID)
	}
	cwd := filepath.Clean(state.CWD)
	// Chat rooms seeded with the install default before the user picked a folder
	// via WORKSPACE (or inherited from a task) should follow _last.json.
	if isChatSessionID(runID) && cwd == filepath.Clean(defaultDir) {
		if last := o.lastWorkspace(); last != "" && last != cwd {
			return last
		}
	}
	return cwd
}

func (o *Orchestrator) persistActorCWD(runID, cwd string) {
	if runID == "" || strings.TrimSpace(cwd) == "" {
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
	o.persistLastWorkspace(cwd)
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
