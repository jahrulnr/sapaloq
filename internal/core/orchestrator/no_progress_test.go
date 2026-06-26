package orchestrator

import "testing"

func TestNormalizeOutcomeForHash(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace collapse", "done   now\n\tlater", "done now later"},
		{"trim trailing dot", "Done.", "Done"},
		{"trim trailing ellipsis", "Done…", "Done"},
		{"trim trailing bang", "Done!", "Done"},
		{"strip called tools echo", "All set. [Called tools: write_file, read_file]", "All set"},
		{"strip tool marker", "Result [Tool: something] after", "Result after"},
		{"strip multiple markers", "a [Called tools: x] b [Called tools: y] c", "a b c"},
		{"half marker dropped", "partial [Called tools: unfinished", "partial"},
		{"no change stable", "Continue the existing task only if a concrete next step", "Continue the existing task only if a concrete next step"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeOutcomeForHash(c.in)
			if got != c.want {
				t.Fatalf("normalizeOutcomeForHash(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeOutcomeForHashParaphraseStability(t *testing.T) {
	// Variants that differ only by whitespace/punctuation/marker echo must
	// hash identically so the no-progress guard does not reset on drift.
	variants := []string{
		"Selesai.",
		"Selesai…",
		"Selesai !",
		"Selesai  .",
		"Selesai. [Called tools: sapaloq_stop]",
		"  Selesai  ",
	}
	first := normalizeOutcomeForHash(variants[0])
	for _, v := range variants[1:] {
		if got := normalizeOutcomeForHash(v); got != first {
			t.Fatalf("variant %q normalized to %q, want %q (same as first)", v, got, first)
		}
	}
	// A genuinely different answer must still hash differently.
	if normalizeOutcomeForHash("Selesai.") == normalizeOutcomeForHash("Masih kerja") {
		t.Fatal("different content must not collide after normalization")
	}
}
