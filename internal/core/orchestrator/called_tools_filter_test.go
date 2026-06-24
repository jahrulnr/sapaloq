package orchestrator

import (
	"strings"
	"testing"
)

// feedAll streams the deltas through a fresh filter and returns the
// concatenation of everything emitted, including the final flush - i.e. the
// text the user would actually see.
func feedAll(deltas ...string) string {
	var f calledToolsFilter
	var b strings.Builder
	for _, d := range deltas {
		b.WriteString(f.feed(d))
	}
	b.WriteString(f.flush())
	return b.String()
}

// TestCalledToolsFilterDropsCompleteMarker: a marker fully contained in one
// delta is removed, surrounding text preserved.
func TestCalledToolsFilterDropsCompleteMarker(t *testing.T) {
	got := feedAll("oke.[Called tools: write_file]")
	if got != "oke." {
		t.Fatalf("complete marker not dropped: %q", got)
	}
}

// TestCalledToolsFilterDropsMarkerSplitAcrossDeltas is the regression for the
// observed orch-chat leak: the marker arrived split across three deltas
// ("…paralel.[Called tools:", " write_file …", "]"). The whole span must be
// dropped, leaving only the prose before it.
func TestCalledToolsFilterDropsMarkerSplitAcrossDeltas(t *testing.T) {
	got := feedAll(
		"\n\nFolder struktur sudah ada. Sekarang aku isi file",
		"-file utamanya secara paralel.[Called tools:",
		" write_file /tmp/profile/index.html, write_file /tmp/profile/css/style.css, write_file /tmp/profile/js",
		"/main.js",
		"]",
	)
	want := "\n\nFolder struktur sudah ada. Sekarang aku isi file-file utamanya secara paralel."
	if got != want {
		t.Fatalf("split marker not dropped cleanly:\n got=%q\nwant=%q", got, want)
	}
}

// TestCalledToolsFilterDropsMarkerWithTrailingText: text after the closing ']'
// must still be emitted.
func TestCalledToolsFilterDropsMarkerWithTrailingText(t *testing.T) {
	got := feedAll("before [Called tools: exec ×2, read_file] after")
	if got != "before  after" {
		t.Fatalf("trailing text lost or marker kept: %q", got)
	}
}

// TestCalledToolsFilterKeepsOrdinaryBrackets: a '[' that does not begin the
// marker must pass through untouched, even when split mid-token.
func TestCalledToolsFilterKeepsOrdinaryBrackets(t *testing.T) {
	cases := [][]string{
		{"lihat [ini] ya"},
		{"array[0] = 1"},
		{"see ", "[note", "] here"},
		{"[Called something else]"},
	}
	for _, deltas := range cases {
		want := strings.Join(deltas, "")
		if got := feedAll(deltas...); got != want {
			t.Fatalf("ordinary bracket altered:\n got=%q\nwant=%q", got, want)
		}
	}
}

// TestCalledToolsFilterFlushReleasesPartialPrefix: if the stream ends while the
// filter is still holding a partial prefix that never became the marker, that
// text must be released on flush (not swallowed).
func TestCalledToolsFilterFlushReleasesPartialPrefix(t *testing.T) {
	if got := feedAll("done [Cal"); got != "done [Cal" {
		t.Fatalf("partial prefix swallowed: %q", got)
	}
	// A bare '[' at end of stream is ordinary text too.
	if got := feedAll("trailing ["); got != "trailing [" {
		t.Fatalf("trailing bracket swallowed: %q", got)
	}
}

// TestCalledToolsFilterDropsUnterminatedMarker: a marker that opens but never
// closes before the stream ends is intentionally discarded (it is our own
// note, not user-facing prose).
func TestCalledToolsFilterDropsUnterminatedMarker(t *testing.T) {
	got := feedAll("oops.[Called tools: write_file, exec")
	if got != "oops." {
		t.Fatalf("unterminated marker leaked: %q", got)
	}
}

// TestCalledToolsFilterMarkerByteAtATime: feeding the marker one byte per delta
// (worst-case fragmentation) still drops it entirely.
func TestCalledToolsFilterMarkerByteAtATime(t *testing.T) {
	full := "hi.[Called tools: exec] bye"
	var f calledToolsFilter
	var b strings.Builder
	for i := 0; i < len(full); i++ {
		b.WriteString(f.feed(full[i : i+1]))
	}
	b.WriteString(f.flush())
	if got := b.String(); got != "hi. bye" {
		t.Fatalf("byte-at-a-time marker not dropped: %q", got)
	}
}

// TestCalledToolsFilterDropsToolMarker is the regression for orch-task-…103:
// MiniMax-M3 emitted a real native tool_call but ALSO wrote a bare "[Tool: …]"
// announce label into its content. The bare label (no "{args}") is not a
// recoverable inline call and must be stripped here so it neither reaches the
// user nor gets echoed back and imitated by the model.
func TestCalledToolsFilterDropsToolMarker(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Let me set up the workspace and build.[Tool: exec]", "Let me set up the workspace and build."},
		{"Directory exists.[Tool: create_file]Now the next step.", "Directory exists.Now the next step."},
		{"before [Tool: sapaloq_fail_task] after", "before  after"},
		// Repeated labels (the spiral) all removed.
		{"x[Tool: sapaloq_fail_task][Tool: sapaloq_fail_task][Tool: sapaloq_fail_task]y", "xy"},
	}
	for _, tc := range cases {
		if got := feedAll(tc.in); got != tc.want {
			t.Fatalf("[Tool: …] not dropped:\n  in=%q\n got=%q\nwant=%q", tc.in, got, tc.want)
		}
	}
}

// TestCalledToolsFilterDropsToolMarkerSplit: the "[Tool: …]" label split across
// deltas (as it streams) is still dropped as a whole.
func TestCalledToolsFilterDropsToolMarkerSplit(t *testing.T) {
	got := feedAll("build the files in parallel.[Tool: create", "_file]", "I need to act now.")
	want := "build the files in parallel.I need to act now."
	if got != want {
		t.Fatalf("split [Tool: …] not dropped cleanly:\n got=%q\nwant=%q", got, want)
	}
}

// TestCalledToolsFilterToolMarkerByteAtATime: worst-case fragmentation of a
// "[Tool: …]" label still drops it entirely.
func TestCalledToolsFilterToolMarkerByteAtATime(t *testing.T) {
	full := "hi.[Tool: exec] bye"
	var f calledToolsFilter
	var b strings.Builder
	for i := 0; i < len(full); i++ {
		b.WriteString(f.feed(full[i : i+1]))
	}
	b.WriteString(f.flush())
	if got := b.String(); got != "hi. bye" {
		t.Fatalf("byte-at-a-time [Tool: …] not dropped: %q", got)
	}
}

// TestCalledToolsFilterKeepsToolLikeProse: brackets that merely resemble the
// "[Tool: …]" marker but diverge must pass through untouched (no over-stripping
// of ordinary text the user wrote or the model legitimately produced).
func TestCalledToolsFilterKeepsToolLikeProse(t *testing.T) {
	cases := [][]string{
		{"[Toolbar] is a UI element"},  // "[Tool" prefix but diverges at 'b'
		{"the [Tools] menu"},           // diverges (no ':')
		{"see ", "[Too", "lkit] docs"}, // split, diverges at 'k'
		{"[Tooling notes] follow"},     // diverges at 'i'
	}
	for _, deltas := range cases {
		want := strings.Join(deltas, "")
		if got := feedAll(deltas...); got != want {
			t.Fatalf("tool-like prose altered:\n got=%q\nwant=%q", got, want)
		}
	}
}
