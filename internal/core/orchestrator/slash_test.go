package orchestrator

import (
	"testing"

	"github.com/jahrulnr/sapaloq/internal/config"
)

func TestFindSlashTokensBoundary(t *testing.T) {
	got := FindSlashTokens("/settings foo/bar pake /settings")
	if len(got) != 2 {
		t.Fatalf("tokens = %#v", got)
	}
}

func TestMatchRegistryClearAlias(t *testing.T) {
	entry, ok := MatchRegistry("/clear", config.DefaultCommands())
	if !ok || entry.ID != "clear" {
		t.Fatalf("entry=%#v ok=%v want clear", entry, ok)
	}
}

func TestMatchRegistrySettingsOnly(t *testing.T) {
	entry, ok := MatchRegistry("cek /settings now", config.DefaultCommands())
	if !ok || entry.ID != "settings" {
		t.Fatalf("entry=%#v ok=%v", entry, ok)
	}
	if _, ok := MatchRegistry("foo/bar", config.DefaultCommands()); ok {
		t.Fatal("unexpected match")
	}
}
