package credentials

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/debug"
)

const (
	envCursorAccessToken = "CURSOR_ACCESS_TOKEN"
	envCursorMachineID   = "CURSOR_MACHINE_ID"
	envCursorGhostMode   = "CURSOR_GHOST_MODE"
)

type Credentials struct {
	AccessToken string
	MachineID   string
	GhostMode   bool
	Source      string
}

type Options struct {
	TokenEnv string
	EnvPaths []string
}

func Load(opts Options) (Credentials, error) {
	tokenEnv := strings.TrimSpace(opts.TokenEnv)
	if tokenEnv == "" {
		tokenEnv = "SAPALOQ_CURSOR_TOKEN"
	}

	accessToken := firstNonEmpty(
		os.Getenv(tokenEnv),
		os.Getenv(envCursorAccessToken),
	)
	machineID := os.Getenv(envCursorMachineID)
	ghostMode := os.Getenv(envCursorGhostMode) != "false"

	if accessToken != "" && machineID != "" {
		creds := Credentials{
			AccessToken: accessToken,
			MachineID:   machineID,
			GhostMode:   ghostMode,
			Source:      "process.env",
		}
		logLoaded(creds)
		return creds, nil
	}

	for _, path := range envFileCandidates(opts) {
		fileEnv := loadEnvFile(path)
		if accessToken == "" {
			accessToken = firstNonEmpty(fileEnv[tokenEnv], fileEnv[envCursorAccessToken])
		}
		if machineID == "" {
			machineID = fileEnv[envCursorMachineID]
		}
		if v, ok := fileEnv[envCursorGhostMode]; ok {
			ghostMode = v != "false"
		}
		if accessToken != "" && machineID != "" {
			creds := Credentials{
				AccessToken: accessToken,
				MachineID:   machineID,
				GhostMode:   ghostMode,
				Source:      path,
			}
			logLoaded(creds)
			return creds, nil
		}
	}

	for _, path := range vscdbCandidates() {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		token, machine, ok := loadVSCDB(path)
		if !ok {
			continue
		}
		if accessToken == "" {
			accessToken = token
		}
		if machineID == "" {
			machineID = machine
		}
		creds := Credentials{
			AccessToken: accessToken,
			MachineID:   machineID,
			GhostMode:   ghostMode,
			Source:      path,
		}
		logLoaded(creds)
		return creds, nil
	}

	if accessToken == "" {
		return Credentials{}, fmt.Errorf(
			"cursor credentials missing: set %s or %s (and %s for machine id)",
			tokenEnv, envCursorAccessToken, envCursorMachineID,
		)
	}
	return Credentials{
		AccessToken: accessToken,
		MachineID:   machineID,
		GhostMode:   ghostMode,
		Source:      sourceLabel(accessToken, machineID),
	}, nil
}

func logLoaded(creds Credentials) {
	debug.Debugf("credentials: source=%s token=%s machine=%s ghost=%v",
		creds.Source, debug.RedactSecret(creds.AccessToken), debug.RedactSecret(creds.MachineID), creds.GhostMode)
}

func sourceLabel(token, machine string) string {
	if machine != "" {
		return "loaded"
	}
	return "loaded (machine id derived at wire)"
}

func expandPath(path string) string {
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

func envFileCandidates(opts Options) []string {
	var paths []string
	seen := map[string]bool{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, expandPath(path))
	}
	for _, path := range opts.EnvPaths {
		add(path)
	}
	if cwd, err := os.Getwd(); err == nil {
		add(filepath.Join(cwd, ".env"))
	}
	add("~/.config/sapaloq/.env")
	return paths
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
