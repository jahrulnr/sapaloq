package credentials

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/debug"

	_ "modernc.org/sqlite"
)

const (
	envCursorAccessToken = "CURSOR_ACCESS_TOKEN"
	envCursorMachineID   = "CURSOR_MACHINE_ID"
	envCursorStateVscdb  = "CURSOR_STATE_VSCDB"
	envCursorGhostMode   = "CURSOR_GHOST_MODE"
)

var (
	accessKeys  = []string{"cursorAuth/accessToken", "cursorAuth/refreshToken"}
	machineKeys = []string{"storage.serviceMachineId", "telemetry.machineId"}
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
	VscdbPath string
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

	for _, path := range vscdbCandidates(opts) {
		vscdb, err := readVscdb(path)
		if err != nil {
			return Credentials{}, err
		}
		if vscdb == nil {
			continue
		}
		if accessToken == "" {
			accessToken = vscdb.AccessToken
		}
		if machineID == "" {
			machineID = vscdb.MachineID
		}
		if accessToken != "" {
			loaded := Credentials{
				AccessToken: accessToken,
				MachineID:   machineID,
				GhostMode:   ghostMode,
				Source:      vscdb.Source,
			}
			logLoaded(loaded)
			return loaded, nil
		}
	}

	if accessToken == "" {
		return Credentials{}, fmt.Errorf(
			"cursor credentials missing: set %s or %s, or login via Cursor IDE (state.vscdb)",
			tokenEnv, envCursorAccessToken,
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

func vscdbCandidates(opts Options) []string {
	if opts.VscdbPath != "" {
		return []string{expandPath(opts.VscdbPath)}
	}
	if path := os.Getenv(envCursorStateVscdb); path != "" {
		return []string{expandPath(path)}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".config/Cursor/User/globalStorage/state.vscdb"),
		filepath.Join(home, ".config/cursor/User/globalStorage/state.vscdb"),
	}
}

type vscdbCreds struct {
	AccessToken string
	MachineID   string
	Source      string
}

func readVscdb(path string) (*vscdbCreds, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	get := func(key string) string {
		var value string
		err := db.QueryRow("SELECT value FROM itemTable WHERE key = ?", key).Scan(&value)
		if err != nil {
			return ""
		}
		return stripQuotes(value)
	}

	accessToken := ""
	for _, key := range accessKeys {
		if v := get(key); v != "" {
			accessToken = v
			break
		}
	}
	if accessToken == "" {
		return nil, nil
	}
	machineID := ""
	for _, key := range machineKeys {
		if v := get(key); v != "" {
			machineID = v
			break
		}
	}
	return &vscdbCreds{
		AccessToken: accessToken,
		MachineID:   machineID,
		Source:      "vscdb:" + path,
	}, nil
}

func stripQuotes(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
