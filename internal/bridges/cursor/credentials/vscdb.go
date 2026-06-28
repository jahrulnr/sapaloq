package credentials

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const envCursorStateVSCDB = "CURSOR_STATE_VSCDB"

var (
	accessTokenKeys = []string{"cursorAuth/accessToken", "cursorAuth/refreshToken"}
	machineIDKeys   = []string{"storage.serviceMachineId", "telemetry.machineId"}
)

func vscdbCandidates() []string {
	if override := strings.TrimSpace(os.Getenv(envCursorStateVSCDB)); override != "" {
		return []string{expandPath(override)}
	}
	var paths []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths,
			filepath.Join(home, ".config/Cursor/User/globalStorage/state.vscdb"),
			filepath.Join(home, ".config/cursor/User/globalStorage/state.vscdb"),
		)
	}
	return paths
}

func loadVSCDB(path string) (accessToken, machineID string, ok bool) {
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return "", "", false
	}
	defer db.Close()

	get := func(key string) string {
		var value string
		err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = ?`, key).Scan(&value)
		if err != nil {
			return ""
		}
		return stripVSCDBQuotes(value)
	}

	for _, key := range accessTokenKeys {
		if accessToken = get(key); accessToken != "" {
			break
		}
	}
	if accessToken == "" {
		return "", "", false
	}
	for _, key := range machineIDKeys {
		if machineID = get(key); machineID != "" {
			break
		}
	}
	return accessToken, machineID, true
}

func stripVSCDBQuotes(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}
	return value
}
