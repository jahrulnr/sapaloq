package credentials

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestLoadFromVSCDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.vscdb")
	seedVSCDB(t, dbPath, map[string]string{
		"cursorAuth/accessToken":     `"vscdb-token"`,
		"storage.serviceMachineId": `"vscdb-machine"`,
	})

	prevToken := os.Getenv("SAPALOQ_CURSOR_TOKEN")
	prevAccess := os.Getenv("CURSOR_ACCESS_TOKEN")
	prevMachine := os.Getenv("CURSOR_MACHINE_ID")
	prevVSCDB := os.Getenv(envCursorStateVSCDB)
	os.Setenv("SAPALOQ_CURSOR_TOKEN", "stale-env-token")
	os.Unsetenv("CURSOR_ACCESS_TOKEN")
	os.Unsetenv("CURSOR_MACHINE_ID")
	os.Setenv(envCursorStateVSCDB, dbPath)
	t.Cleanup(func() {
		restoreEnv("SAPALOQ_CURSOR_TOKEN", prevToken)
		restoreEnv("CURSOR_ACCESS_TOKEN", prevAccess)
		restoreEnv("CURSOR_MACHINE_ID", prevMachine)
		restoreEnv(envCursorStateVSCDB, prevVSCDB)
	})

	creds, err := Load(Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != "vscdb-token" || creds.MachineID != "vscdb-machine" {
		t.Fatalf("creds = %#v", creds)
	}
	if creds.Source != dbPath {
		t.Fatalf("source = %q want %q", creds.Source, dbPath)
	}
}

func seedVSCDB(t *testing.T, path string, rows map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	for key, value := range rows {
		if _, err := db.Exec(`INSERT INTO ItemTable (key, value) VALUES (?, ?)`, key, value); err != nil {
			t.Fatal(err)
		}
	}
}
