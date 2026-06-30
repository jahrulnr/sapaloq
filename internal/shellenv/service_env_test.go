package shellenv_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/shellenv"
)

func TestServiceLikeEnvImportsConfiguredCredential(t *testing.T) {
	if os.Getenv("SAPALOQ_SHELLENV_E2E") != "1" {
		t.Skip("set SAPALOQ_SHELLENV_E2E=1")
	}
	cfgPath := config.ConfigPath(os.Getenv("SAPALOQ_CONFIG"), config.DefaultConfig())
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := cfg.LLMBridge.ActiveProvider()
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(entry.CredentialsEnv)
	if key == "" {
		t.Skip("active provider has no credentialsEnv in config")
	}

	pidBytes, err := exec.Command("systemctl", "--user", "show", "sapaloq.service", "-p", "MainPID", "--value").Output()
	if err != nil {
		t.Skipf("systemctl unavailable: %v", err)
	}
	pid := strings.TrimSpace(string(pidBytes))
	raw, err := os.ReadFile("/proc/" + pid + "/environ")
	if err != nil {
		t.Fatalf("read service environ: %v", err)
	}
	os.Clearenv()
	for _, rec := range strings.Split(string(raw), "\x00") {
		if rec == "" || !strings.Contains(rec, "=") {
			continue
		}
		k, v, _ := strings.Cut(rec, "=")
		if k == key {
			continue
		}
		os.Setenv(k, v)
	}
	os.Unsetenv(key)
	t.Cleanup(func() { os.Unsetenv(key) })

	shellenv.LoadOnce()
	if os.Getenv(key) == "" {
		t.Fatalf("%s still empty after LoadOnce with service-like env", key)
	}
}
