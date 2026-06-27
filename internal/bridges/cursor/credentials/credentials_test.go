package credentials

import (
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

func restoreEnv(key, value string) {
	if value == "" {
		os.Unsetenv(key)
		return
	}
	os.Setenv(key, value)
}
