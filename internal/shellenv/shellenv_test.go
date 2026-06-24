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

func TestIsRelevant(t *testing.T) {
	for _, k := range []string{"SAPALOQ_X", "CURSOR_ACCESS_TOKEN", "BLACKBOX_API_KEY", "OPENAI_API_KEY"} {
		if !isRelevant(k) {
			t.Fatalf("%q should be relevant", k)
		}
	}
	for _, k := range []string{"PATH", "HOME", "PS1", "RANDOM_SECRET"} {
		if isRelevant(k) {
			t.Fatalf("%q should NOT be relevant", k)
		}
	}
}

func TestApplyEnvSkipsAlreadySetAndIrrelevant(t *testing.T) {
	t.Setenv("SAPALOQ_PRESET", "keep-me")
	// SAPALOQ_NEW absent; ensure clean.
	os.Unsetenv("SAPALOQ_NEW")
	t.Cleanup(func() { os.Unsetenv("SAPALOQ_NEW") })

	applyEnv(map[string]string{
		"SAPALOQ_PRESET": "should-not-override",
		"SAPALOQ_NEW":    "imported",
		"PATH":           "/evil/bin", // irrelevant prefix → ignored
	})

	if got := os.Getenv("SAPALOQ_PRESET"); got != "keep-me" {
		t.Fatalf("preset overridden: %q", got)
	}
	if got := os.Getenv("SAPALOQ_NEW"); got != "imported" {
		t.Fatalf("new var not imported: %q", got)
	}
	if got := os.Getenv("PATH"); got == "/evil/bin" {
		t.Fatalf("irrelevant PATH should not have been imported")
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

// TestLoadEndToEnd drives the full load() (bash rc → process env) with a temp
// HOME, proving the wiring main() relies on works.
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

	load() // call the unexported worker directly (bypasses the process-wide sync.Once)

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
		"export NOT_RELEVANT='should-be-ignored-by-caller'\n" +
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
}
