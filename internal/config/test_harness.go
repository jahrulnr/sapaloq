package config

import (
	"path/filepath"
	"testing"
)

// WriteTestConfig materializes an isolated config + socket under t.TempDir(),
// sets SAPALOQ_CONFIG for the test process, and returns dataDir, cfgPath, socketPath.
// Every go test / e2e harness should use this instead of DefaultConfig() alone.
func WriteTestConfig(t *testing.T, updatedBy string) (dataDir, cfgPath, socketPath string) {
	t.Helper()
	dataDir = t.TempDir()
	socketPath = filepath.Join(dataDir, "run", TestSocketFileName)
	cfgPath = filepath.Join(dataDir, "config.json")
	if err := SaveRaw(cfgPath, map[string]any{
		"schemaVersion": "1.0.0",
		"runtime":       map[string]any{"dataDir": dataDir},
		"events":        map[string]any{"bus": map[string]any{"socketPath": socketPath}},
	}, updatedBy); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAPALOQ_CONFIG", cfgPath)
	t.Setenv("SAPALOQ_TEST_ISOLATION", "1")
	return dataDir, cfgPath, socketPath
}
