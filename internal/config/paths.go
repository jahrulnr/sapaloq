package config

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultConfigDir = "~/.config/sapaloq"
	defaultDataDir   = "~/SapaLOQ"
)

func DefaultConfigDir() string { return ExpandPath(defaultConfigDir) }
func DefaultDataDir() string   { return ExpandPath(defaultDataDir) }

func ExpandPath(path string) string {
	if path == "" {
		return path
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func RuntimeDirs(cfg Config) RuntimeDirsInfo {
	dataDir := ExpandPath(cfg.Runtime.DataDir)
	if dataDir == "" {
		dataDir = ExpandPath(defaultDataDir)
	}
	return RuntimeDirsInfo{
		DataDir:      dataDir,
		RunDir:       filepath.Join(dataDir, "run"),
		MemoryDir:    filepath.Join(dataDir, "memory"),
		StateDir:     filepath.Join(dataDir, "state"),
		RolloutDir:   filepath.Join(dataDir, "state", "rollout"),
		SessionsDir:  filepath.Join(dataDir, "state", "sessions"),
		TasksDir:     filepath.Join(dataDir, "state", "tasks"),
		ProgressDir:  filepath.Join(dataDir, "state", "rollout"), // alias: canonical rollout JSONL
		WorkersDir:   filepath.Join(dataDir, "state", "workers"),
		VaultDir:     filepath.Join(dataDir, "vault"),
		WorkspaceDir: filepath.Join(dataDir, "workspace"),
		PromptsDir:   filepath.Join(dataDir, "prompts"),
		SkillsDir:    filepath.Join(dataDir, "skills"),
		EtcDir:       filepath.Join(dataDir, "etc"),
		SocketPath:   ExpandPath(cfg.Events.Bus.SocketPath),
	}
}

type RuntimeDirsInfo struct {
	DataDir string
	RunDir  string
	// MemoryDir is the legacy memory root (companion.db migration only).
	MemoryDir string
	// StateDir holds transient runtime/orchestration state and JSON persistence.
	StateDir string
	// RolloutDir holds canonical session rollout JSONL (conversation + tool stream).
	RolloutDir string
	// SessionsDir holds session index and per-session turn/checkpoint JSON.
	SessionsDir string
	// TasksDir holds per-task records (status.json, plan.md). One subdir per
	// task id. Lives under StateDir.
	TasksDir    string
	ProgressDir string // deprecated alias for RolloutDir
	// WorkersDir holds per-worker observability artifacts: error logs and the
	// worker-registry snapshot. One subdir per task id. Lives under StateDir.
	WorkersDir   string
	VaultDir     string
	WorkspaceDir string
	PromptsDir   string
	SkillsDir    string
	EtcDir       string
	SocketPath   string
}

func EnsureRuntimeDirs(dirs RuntimeDirsInfo) error {
	for _, dir := range []string{dirs.DataDir, dirs.RunDir, dirs.MemoryDir, dirs.StateDir, dirs.RolloutDir, dirs.SessionsDir, dirs.TasksDir, dirs.ProgressDir, dirs.WorkersDir, dirs.VaultDir, dirs.WorkspaceDir, dirs.PromptsDir, dirs.SkillsDir, dirs.EtcDir} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		// MkdirAll preserves an existing broader mode. Runtime directories hold
		// chat history, tool arguments and IPC endpoints, so tighten them even
		// after upgrading from older releases that created them as 0755.
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}
