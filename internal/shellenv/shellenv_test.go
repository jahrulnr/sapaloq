package shellenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseNulEnv(t *testing.T) {
	in := []byte("A=1\x00B=two words\x00C=line1\nline2\x00=skip\x00D=\x00")
	got := parseNulEnv(in)
	if got["A"] != "1" || got["B"] != "two words" || got["C"] != "line1\nline2" || got["D"] != "" {
		t.Fatalf("parseNulEnv = %#v", got)
	}
	if _, ok := got[""]; ok {
		t.Fatalf("empty key must be skipped: %#v", got)
	}
}

func TestApplyEnvSkipsAlreadySet(t *testing.T) {
	t.Setenv("SAPALOQ_PRESET", "keep-me")
	os.Unsetenv("SAPALOQ_NEW")
	os.Unsetenv("INI_EXPERIMENT_APIKEY")
	os.Unsetenv("PATH")
	t.Cleanup(func() {
		os.Unsetenv("SAPALOQ_NEW")
		os.Unsetenv("INI_EXPERIMENT_APIKEY")
		os.Unsetenv("PATH")
	})

	applyEnv(map[string]string{
		"SAPALOQ_PRESET":         "should-not-override",
		"SAPALOQ_NEW":            "imported",
		"INI_EXPERIMENT_APIKEY":  "custom-provider",
		"PATH":                   "/custom/bin",
	})

	if got := os.Getenv("SAPALOQ_PRESET"); got != "keep-me" {
		t.Fatalf("preset overridden: %q", got)
	}
	if got := os.Getenv("SAPALOQ_NEW"); got != "imported" {
		t.Fatalf("new var not imported: %q", got)
	}
	if got := os.Getenv("INI_EXPERIMENT_APIKEY"); got != "custom-provider" {
		t.Fatalf("custom api key not imported: %q", got)
	}
	if got := os.Getenv("PATH"); got != "/custom/bin" {
		t.Fatalf("PATH not imported when unset: %q", got)
	}
}

func TestApplyEnvOverridesEmptyPlaceholder(t *testing.T) {
	const key = "SAPALOQ_EMPTY_PLACEHOLDER"
	t.Setenv(key, "")
	t.Cleanup(func() { os.Unsetenv(key) })

	applyEnv(map[string]string{key: "from-rc"})
	if got := os.Getenv(key); got != "from-rc" {
		t.Fatalf("empty placeholder not overridden: %q", got)
	}
}

func TestSourceShellRCMissingFileIsSilent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("shell-rc sourcing is linux-only")
	}
	_, ok := sourceShellRC(shellRC{shell: "bash", rc: filepath.Join(t.TempDir(), "nope.bashrc")})
	if ok {
		t.Fatalf("missing rc must return ok=false")
	}
}

func TestLoadEndToEnd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("shell-rc sourcing is linux-only")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".bashrc"),
		[]byte("export SAPALOQ_E2E_PROBE='from-rc'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	os.Unsetenv("SAPALOQ_E2E_PROBE")
	t.Cleanup(func() { os.Unsetenv("SAPALOQ_E2E_PROBE") })

	reload()

	if got := os.Getenv("SAPALOQ_E2E_PROBE"); got != "from-rc" {
		t.Fatalf("SAPALOQ_E2E_PROBE = %q, want from-rc", got)
	}
}

func TestSourceShellRCImportsExportedVar(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("shell-rc sourcing is linux-only")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	rc := filepath.Join(t.TempDir(), ".bashrc")
	body := "export SAPALOQ_FROM_RC='rc-token'\n" +
		"export INI_EXPERIMENT_APIKEY='any-name'\n" +
		"echo 'noise to stderr' 1>&2\n"
	if err := os.WriteFile(rc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	env, ok := sourceShellRC(shellRC{shell: "bash", rc: rc})
	if !ok {
		t.Fatalf("expected ok=true sourcing a valid rc")
	}
	if env["SAPALOQ_FROM_RC"] != "rc-token" {
		t.Fatalf("exported var not captured: %#v", env["SAPALOQ_FROM_RC"])
	}
	if env["INI_EXPERIMENT_APIKEY"] != "any-name" {
		t.Fatalf("custom key not captured: %#v", env["INI_EXPERIMENT_APIKEY"])
	}
}

func TestSourceShellRCPassesInteractiveGuard(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("shell-rc sourcing is linux-only")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	rc := filepath.Join(t.TempDir(), ".bashrc")
	body := "# If not running interactively, don't do anything\n" +
		"case $- in\n" +
		"    *i*) ;;\n" +
		"      *) return;;\n" +
		"esac\n" +
		"\n" +
		"export SAPALOQ_GUARDED_TOKEN='past-the-guard'\n"
	if err := os.WriteFile(rc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	env, ok := sourceShellRC(shellRC{shell: "bash", rc: rc})
	if !ok {
		t.Fatalf("expected ok=true sourcing a guarded rc")
	}
	if env["SAPALOQ_GUARDED_TOKEN"] != "past-the-guard" {
		t.Fatalf("export below the interactive guard was not captured: %#v", env["SAPALOQ_GUARDED_TOKEN"])
	}
}

func TestLoadDotEnvImportsAllKeys(t *testing.T) {
	home := t.TempDir()
	dotenvDir := filepath.Join(home, ".config", "sapaloq")
	if err := os.MkdirAll(dotenvDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const key = "SAPALOQ_DOTENV_PROBE"
	if err := os.WriteFile(filepath.Join(dotenvDir, ".env"),
		[]byte(key+"=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv(key)
	t.Cleanup(func() { os.Unsetenv(key) })

	loadDotEnvFiles(home)

	if got := os.Getenv(key); got != "from-dotenv" {
		t.Fatalf("%s = %q, want from-dotenv", key, got)
	}
}
