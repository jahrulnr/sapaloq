package orchestrator

import (
	"strings"
	"testing"
)

// feedAll streams `full` through a fresh scrubber in fragments of size `step`
// (to mimic SSE deltas), then flushes, returning the concatenated visible text.
func feedAll(full string, step int) string {
	var s responseScrubber
	var out strings.Builder
	for i := 0; i < len(full); i += step {
		end := i + step
		if end > len(full) {
			end = len(full)
		}
		out.WriteString(s.feed(full[i:end]))
	}
	out.WriteString(s.flush())
	return out.String()
}

func TestResponseScrubberStripsLeadingLabels(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "the leaked [Tool Results] label (mixed case) + newline",
			in:   "[Tool Results]\nPantesan kelihatan \"kosongan\" ya — kamu bener.",
			want: "Pantesan kelihatan \"kosongan\" ya — kamu bener.",
		},
		{
			name: "lowercase [tool results]",
			in:   "[tool results]\nreal answer",
			want: "real answer",
		},
		{
			name: "singular [Tool result]",
			in:   "[Tool result]\nok",
			want: "ok",
		},
		{
			name: "[Usage] readout line stripped to end of line",
			in:   "[Usage] turn 3 · tool-calls so far 2\nHere is the plan.",
			want: "Here is the plan.",
		},
		{
			name: "no label passes through unchanged",
			in:   "Halo, ini jawaban biasa tanpa label.",
			want: "Halo, ini jawaban biasa tanpa label.",
		},
		{
			name: "bracket later in the answer is untouched",
			in:   "Step one [see note] then continue.",
			want: "Step one [see note] then continue.",
		},
		{
			name: "non-label leading bracket is preserved",
			in:   "[note] something the user actually wrote",
			want: "[note] something the user actually wrote",
		},
		{
			name: "label with no following newline (whole-line answer)",
			in:   "[Tool Results] inline rest",
			want: " inline rest",
		},
		{
			name: "leading whitespace before a real answer is preserved",
			in:   "\n  already-indented answer",
			want: "\n  already-indented answer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Test several fragmentations, including 1-byte deltas that split
			// the label across many events (the real SSE failure mode).
			for _, step := range []int{1, 2, 3, 7, len(tc.in)} {
				if step <= 0 {
					step = 1
				}
				got := feedAll(tc.in, step)
				if got != tc.want {
					t.Errorf("step=%d: got %q want %q", step, got, tc.want)
				}
			}
		})
	}
}

// TestResponseScrubberPassThroughAfterLead ensures that once the leading region
// is resolved, later content containing labels is NOT stripped (the anchor is
// start-of-turn only).
func TestResponseScrubberPassThroughAfterLead(t *testing.T) {
	var s responseScrubber
	var out strings.Builder
	out.WriteString(s.feed("Answer begins. "))
	out.WriteString(s.feed("[Tool Results] should stay mid-stream "))
	out.WriteString(s.feed("[Usage] too"))
	out.WriteString(s.flush())
	got := out.String()
	want := "Answer begins. [Tool Results] should stay mid-stream [Usage] too"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestResponseScrubberUsageOnlyTurn covers a turn whose entire visible output
// is just the echoed [Usage] label with no trailing newline/answer — it should
// be dropped completely at flush rather than leaked.
func TestResponseScrubberUsageOnlyTurn(t *testing.T) {
	got := feedAll("[Usage] turn 5 · tool-calls so far 4", 4)
	if got != "" {
		t.Fatalf("usage-only turn must be fully stripped, got %q", got)
	}
}
