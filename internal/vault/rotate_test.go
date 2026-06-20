package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeN appends n entries with a recognizable RawName index.
func writeN(t *testing.T, w *Writer, start, n int) {
	t.Helper()
	for i := start; i < start+n; i++ {
		if err := w.Append(Entry{RawName: fmt.Sprintf("t%04d", i), Reason: "executed"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRotationTriggersAndCascades(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool-calls.jsonl")
	// Tiny cap so a handful of lines forces multiple rotations; keep 2 siblings.
	w, err := NewWithOptions(path, Options{MaxBytes: 200, KeepFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	// Each line is well over a few dozen bytes; write enough to rotate twice.
	writeN(t, w, 0, 30)

	// Primary must exist and stay under (or near) the cap after rotation.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("primary missing: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("primary should hold the latest line(s)")
	}

	// At most KeepFiles rotated siblings exist; .3 must NOT exist (keep=2).
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected .1 sibling after rotation: %v", err)
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Fatalf(".3 should have been dropped (KeepFiles=2)")
	}
}

func TestRotationKeepsMostRecentReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool-calls.jsonl")
	// Use a cap large enough that several entries land per file and the kept
	// window (primary + 3 siblings) comfortably exceeds 10 entries, so we can
	// assert that ReadRecent spans rotated files to find the newest 10.
	w, err := NewWithOptions(path, Options{MaxBytes: 1024, KeepFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	writeN(t, w, 0, 50)

	// Sanity: rotation actually happened (a sibling exists).
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotation to have occurred: %v", err)
	}
	// The primary alone should hold fewer than 10 lines (proving ReadRecent
	// must span siblings to satisfy the request).
	primaryOnly, err := ReadEntries(path, 0)
	if err != nil {
		t.Fatal(err)
	}

	recent, err := ReadRecent(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 10 {
		t.Fatalf("expected 10 recent entries, got %d (primary had %d)", len(recent), len(primaryOnly))
	}
	// The very last write was t0049; it must be the last entry and the window
	// must be contiguous & ascending up to it.
	last := recent[len(recent)-1]
	if last.RawName != "t0049" {
		t.Fatalf("expected newest entry t0049 last, got %q", last.RawName)
	}
	if recent[0].RawName != "t0040" {
		t.Fatalf("expected oldest-of-10 to be t0040, got %q", recent[0].RawName)
	}
}

func TestDefaultsAppliedViaNew(t *testing.T) {
	dir := t.TempDir()
	w, err := New(filepath.Join(dir, "tc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if w.opts.MaxBytes != DefaultMaxBytes || w.opts.KeepFiles != DefaultKeepFiles {
		t.Fatalf("New should apply defaults, got %#v", w.opts)
	}
}

func TestZeroOptionsAreDefaulted(t *testing.T) {
	o := Options{}.withDefaults()
	if o.MaxBytes != DefaultMaxBytes || o.KeepFiles != DefaultKeepFiles {
		t.Fatalf("zero Options not defaulted: %#v", o)
	}
}

func TestNoRotationUnderCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool-calls.jsonl")
	w, err := NewWithOptions(path, Options{MaxBytes: 1 << 20, KeepFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	writeN(t, w, 0, 5)
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatalf("should not rotate when well under cap")
	}
	entries, err := ReadEntries(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries in primary, got %d", len(entries))
	}
}
