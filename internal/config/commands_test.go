package config

import "testing"

func sampleProviders() []LLMBridge {
	return []LLMBridge{
		{Key: "minimax-free", Driver: "blackboxai", Model: "minimax"},
		{Key: "minimax-pro", Driver: "blackboxai", Model: "minimax-pro"},
		{Key: "kimi", Driver: "moonshot", Model: "kimi-k2"},
	}
}

// Typing "/model" (command fully typed, no trailing space yet) should already
// list every configured provider - this is the bug where suggestions only
// appeared after a trailing space.
func TestSuggestModelWithoutTrailingSpace(t *testing.T) {
	cmds := DefaultCommands()
	got := cmds.SuggestWithProviders("/model", sampleProviders())
	if len(got) != 3 {
		t.Fatalf("expected 3 provider suggestions for %q, got %d: %+v", "/model", len(got), got)
	}
	if got[0].Prefix != "/model minimax-free" || got[0].Category != "models" {
		t.Fatalf("unexpected first suggestion: %+v", got[0])
	}
}

// A trailing space must behave the same as no space (all providers).
func TestSuggestModelWithTrailingSpace(t *testing.T) {
	cmds := DefaultCommands()
	got := cmds.SuggestWithProviders("/model ", sampleProviders())
	if len(got) != 3 {
		t.Fatalf("expected 3 provider suggestions for %q, got %d", "/model ", len(got))
	}
}

// Once the user starts typing a provider key the list is prefix-filtered.
func TestSuggestModelPrefixFilter(t *testing.T) {
	cmds := DefaultCommands()
	got := cmds.SuggestWithProviders("/model minimax", sampleProviders())
	if len(got) != 2 {
		t.Fatalf("expected 2 minimax* providers, got %d: %+v", len(got), got)
	}
	for _, entry := range got {
		if entry.Prefix != "/model minimax-free" && entry.Prefix != "/model minimax-pro" {
			t.Fatalf("unexpected filtered suggestion: %+v", entry)
		}
	}
}

// Partial command names ("/mod") still surface the Model registry entry so the
// command itself remains discoverable before it is fully typed.
func TestSuggestPartialCommandName(t *testing.T) {
	cmds := DefaultCommands()
	got := cmds.SuggestWithProviders("/mod", sampleProviders())
	if len(got) != 1 || got[0].ID != "model" || got[0].Prefix != "/model" {
		t.Fatalf("expected the Model command entry for %q, got %+v", "/mod", got)
	}
}

// Typing "/thinking" should list every reasoning level without a trailing space.
func TestSuggestThinkingWithoutTrailingSpace(t *testing.T) {
	cmds := DefaultCommands()
	got := cmds.SuggestWithProviders("/thinking", nil)
	if len(got) != len(ThinkingLevels) {
		t.Fatalf("expected %d thinking levels, got %d: %+v", len(ThinkingLevels), len(got), got)
	}
	if got[0].Prefix != "/thinking low" {
		t.Fatalf("unexpected first thinking suggestion: %+v", got[0])
	}
}

func TestSuggestThinkingPrefixFilter(t *testing.T) {
	cmds := DefaultCommands()
	got := cmds.SuggestWithProviders("/thinking h", nil)
	if len(got) != 1 || got[0].Prefix != "/thinking high" {
		t.Fatalf("expected only /thinking high, got %+v", got)
	}
}

// The bare trigger lists the top-level commands.
func TestSuggestTopLevel(t *testing.T) {
	cmds := DefaultCommands()
	got := cmds.SuggestWithProviders("/", sampleProviders())
	if len(got) != len(DefaultCommands().Registry) {
		t.Fatalf("expected all %d commands for %q, got %d", len(DefaultCommands().Registry), "/", len(got))
	}
}
