package config

import (
	"os"
	"path/filepath"
	"strings"
)

const defaultDataDir = "~/.config/sapaloq"

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
		DataDir:     dataDir,
		RunDir:      filepath.Join(dataDir, "run"),
		MemoryDir:   filepath.Join(dataDir, "memory"),
		ProgressDir: filepath.Join(dataDir, "memory", "progress"),
		VaultDir:    filepath.Join(dataDir, "vault"),
		SocketPath:  ExpandPath(cfg.Events.Bus.SocketPath),
	}
}

type RuntimeDirsInfo struct {
	DataDir     string
	RunDir      string
	MemoryDir   string
	ProgressDir string
	VaultDir    string
	SocketPath  string
}

func EnsureRuntimeDirs(dirs RuntimeDirsInfo) error {
	for _, dir := range []string{dirs.DataDir, dirs.RunDir, dirs.MemoryDir, dirs.ProgressDir, dirs.VaultDir} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}
