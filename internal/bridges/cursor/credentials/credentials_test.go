package credentials

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	got := parseEnvFile(`# comment
SAPALOQ_CURSOR_TOKEN=abc123
CURSOR_MACHINE_ID="uuid-here"
`)
	if got["SAPALOQ_CURSOR_TOKEN"] != "abc123" || got["CURSOR_MACHINE_ID"] != "uuid-here" {
		t.Fatalf("got = %#v", got)
	}
}

func TestLoadFromEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	prevToken := os.Getenv("SAPALOQ_CURSOR_TOKEN")
	prevAccess := os.Getenv("CURSOR_ACCESS_TOKEN")
	prevMachine := os.Getenv("CURSOR_MACHINE_ID")
	os.Unsetenv("SAPALOQ_CURSOR_TOKEN")
	os.Unsetenv("CURSOR_ACCESS_TOKEN")
	os.Unsetenv("CURSOR_MACHINE_ID")
	t.Cleanup(func() {
		restoreEnv("SAPALOQ_CURSOR_TOKEN", prevToken)
		restoreEnv("CURSOR_ACCESS_TOKEN", prevAccess)
		restoreEnv("CURSOR_MACHINE_ID", prevMachine)
	})
	if err := os.WriteFile(envPath, []byte("SAPALOQ_CURSOR_TOKEN=file-token\nCURSOR_MACHINE_ID=file-machine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	creds, err := Load(Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN", EnvPaths: []string{envPath}})
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != "file-token" || creds.MachineID != "file-machine" {
		t.Fatalf("creds = %#v", creds)
	}
}

func TestLoadFromVscdb(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.vscdb")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE itemTable (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO itemTable(key,value) VALUES (?,?)`, "cursorAuth/accessToken", `"vscdb-token"`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO itemTable(key,value) VALUES (?,?)`, "storage.serviceMachineId", `"machine-uuid"`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	prevToken := os.Getenv("SAPALOQ_CURSOR_TOKEN")
	prevAccess := os.Getenv("CURSOR_ACCESS_TOKEN")
	prevMachine := os.Getenv("CURSOR_MACHINE_ID")
	os.Unsetenv("SAPALOQ_CURSOR_TOKEN")
	os.Unsetenv("CURSOR_ACCESS_TOKEN")
	os.Unsetenv("CURSOR_MACHINE_ID")
	t.Cleanup(func() {
		restoreEnv("SAPALOQ_CURSOR_TOKEN", prevToken)
		restoreEnv("CURSOR_ACCESS_TOKEN", prevAccess)
		restoreEnv("CURSOR_MACHINE_ID", prevMachine)
	})

	creds, err := Load(Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN", VscdbPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != "vscdb-token" || creds.MachineID != "machine-uuid" {
		t.Fatalf("creds = %#v", creds)
	}
	if creds.Source != "vscdb:"+dbPath {
		t.Fatalf("source = %q", creds.Source)
	}
}

func restoreEnv(key, value string) {
	if value == "" {
		os.Unsetenv(key)
		return
	}
	os.Setenv(key, value)
}
