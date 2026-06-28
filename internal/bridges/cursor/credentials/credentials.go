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
	AccessToken  string
	RefreshToken string
	MachineID    string
	GhostMode    bool
	Source       string
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

	envToken := firstNonEmpty(os.Getenv(tokenEnv), os.Getenv(envCursorAccessToken))
	envRefresh := strings.TrimSpace(os.Getenv("CURSOR_REFRESH_TOKEN"))
	envMachine := strings.TrimSpace(os.Getenv(envCursorMachineID))
	ghostMode := os.Getenv(envCursorGhostMode) != "false"

	// Explicit headless override: both token and machine id in process env.
	if envToken != "" && envMachine != "" {
		creds := Credentials{
			AccessToken:  envToken,
			RefreshToken: envRefresh,
			MachineID:    envMachine,
			GhostMode:    ghostMode,
			Source:       "process.env",
		}
		logLoaded(creds)
		return creds, nil
	}

	// Prefer the live Cursor IDE session over a stale SAPALOQ_CURSOR_TOKEN that
	// only has a token (common when credentialsEnv is set but the env var was
	// copied once and never refreshed).
	if creds, ok := loadFromVSCDB(ghostMode); ok {
		logLoaded(creds)
		return creds, nil
	}

	accessToken := envToken
	refreshToken := envRefresh
	machineID := envMachine

	for _, path := range envFileCandidates(opts) {
		fileEnv := loadEnvFile(path)
		if accessToken == "" {
			accessToken = firstNonEmpty(fileEnv[tokenEnv], fileEnv[envCursorAccessToken])
		}
		if refreshToken == "" {
			refreshToken = strings.TrimSpace(fileEnv["CURSOR_REFRESH_TOKEN"])
		}
		if machineID == "" {
			machineID = fileEnv[envCursorMachineID]
		}
		if v, ok := fileEnv[envCursorGhostMode]; ok {
			ghostMode = v != "false"
		}
		if accessToken != "" && machineID != "" {
			creds := Credentials{
				AccessToken:  accessToken,
				RefreshToken: refreshToken,
				MachineID:    machineID,
				GhostMode:    ghostMode,
				Source:       path,
			}
			logLoaded(creds)
			return creds, nil
		}
	}

	if accessToken == "" {
		return Credentials{}, fmt.Errorf(
			"cursor credentials missing: set %s or %s (and %s for machine id), or log in via Cursor IDE",
			tokenEnv, envCursorAccessToken, envCursorMachineID,
		)
	}
	creds := Credentials{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		MachineID:    machineID,
		GhostMode:    ghostMode,
		Source:       sourceLabel(accessToken, machineID),
	}
	logLoaded(creds)
	return creds, nil
}

func loadFromVSCDB(ghostMode bool) (Credentials, bool) {
	for _, path := range vscdbCandidates() {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		token, refresh, machine, ok := loadVSCDB(path)
		if !ok {
			continue
		}
		return Credentials{
			AccessToken:  token,
			RefreshToken: refresh,
			MachineID:    machine,
			GhostMode:    ghostMode,
			Source:       path,
		}, true
	}
	return Credentials{}, false
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
