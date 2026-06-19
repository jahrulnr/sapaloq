package cursor

import (
	"os"
	"path/filepath"
	"testing"
)

func forceMockCredentials(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing-state.vscdb")
	t.Setenv("CURSOR_STATE_VSCDB", missing)
	t.Setenv("SAPALOQ_CURSOR_TOKEN", "")
	t.Setenv("CURSOR_ACCESS_TOKEN", "")
	t.Setenv("CURSOR_MACHINE_ID", "")
}

func restoreCredentialEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	for key, val := range vars {
		if val == "" {
			os.Unsetenv(key)
		} else {
			os.Setenv(key, val)
		}
	}
}

func snapshotCredentialEnv() map[string]string {
	keys := []string{
		"SAPALOQ_CURSOR_TOKEN",
		"CURSOR_ACCESS_TOKEN",
		"CURSOR_MACHINE_ID",
		"CURSOR_STATE_VSCDB",
	}
	out := map[string]string{}
	for _, key := range keys {
		out[key], _ = os.LookupEnv(key)
	}
	return out
}
