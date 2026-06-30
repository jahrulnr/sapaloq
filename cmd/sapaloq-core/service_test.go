package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderUnitIncludesDotenv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := renderUnit("/usr/bin/sapaloq-core", "/tmp/cfg.json")
	want := filepath.Join(home, ".config", "sapaloq", ".env")
	if !strings.Contains(got, "EnvironmentFile=-"+want) {
		t.Fatalf("unit missing EnvironmentFile:\n%s", got)
	}
	if !strings.Contains(got, "After=network.target") {
		t.Fatalf("unit missing network ordering:\n%s", got)
	}
	if !strings.Contains(got, "ExecStart=/usr/bin/sapaloq-core run") {
		t.Fatalf("unit missing ExecStart:\n%s", got)
	}
}

func TestRenderWidgetDesktopEntryExec(t *testing.T) {
	got := renderWidgetDesktopEntry("/opt/sapaloq-widget")
	if !strings.Contains(got, "Exec=/opt/sapaloq-widget") {
		t.Fatalf("autostart missing widget exec:\n%s", got)
	}
}
