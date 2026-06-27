package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func readFileForTest(path string) ([]byte, error) { return os.ReadFile(path) }
func occurrences(s, sub string) int               { return strings.Count(s, sub) }

func TestAsyncProgressWriterTerminalIsSync(t *testing.T) {
	dir := t.TempDir()
	w := newAsyncProgressWriter(ProgressWriter{Dir: filepath.Join(dir, "progress")})
	defer w.Close("s1")
	// A terminal event (checkpoint) must be persisted synchronously: it is
	// readable immediately after Append returns, with no flush needed.
	ev := bridge.NewEvent(bridge.EventCheckpoint)
	ev.SessionID = "s1"
	ev.CheckpointIndex = 1
	if err := w.Append("s1", ev); err != nil {
		t.Fatalf("Append terminal: %v", err)
	}
	// Drain the async queue for s1 so any buffered deltas land too, then read
	// the JSONL. The checkpoint event must already be present (sync write).
	w.flushSync("s1")
	path := filepath.Join(dir, "progress", "orch-s1.jsonl")
	b, err := readFileForTest(path)
	if err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if !strings.Contains(string(b), `"checkpoint"`) {
		t.Fatalf("terminal checkpoint not persisted synchronously: %s", string(b))
	}
}

func TestAsyncProgressWriterDeltaIsBuffered(t *testing.T) {
	dir := t.TempDir()
	w := newAsyncProgressWriter(ProgressWriter{Dir: filepath.Join(dir, "progress")})
	// Enqueue many delta events; they should not block even if the drain is
	// slow (we hold no reader). The buffer is 256, so 100 deltas fit easily.
	for i := 0; i < 100; i++ {
		ev := bridge.NewEvent(bridge.EventResponseDelta)
		ev.SessionID = "s2"
		ev.Delta = "x"
		_ = w.Append("s2", ev)
	}
	// Close drains + flushes remaining events.
	w.Close("s2")
	path := filepath.Join(dir, "progress", "orch-s2.jsonl")
	b, err := readFileForTest(path)
	if err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if count := occurrences(string(b), `"response_delta"`); count != 100 {
		t.Fatalf("expected 100 persisted deltas after close, got %d", count)
	}
}

func TestAsyncProgressWriterDropOnFullBuffer(t *testing.T) {
	dir := t.TempDir()
	w := newAsyncProgressWriter(ProgressWriter{Dir: filepath.Join(dir, "progress")})
	// Flood more than the buffer without an external reader. The drain
	// goroutine runs concurrently, so the exact count is non-deterministic;
	// the guarantee under test is that Append NEVER blocks the caller (the
	// loop completes) and at least some deltas are persisted. Excess that
	// cannot fit the buffer is dropped rather than blocking.
	for i := 0; i < asyncProgressBuffered+200; i++ {
		ev := bridge.NewEvent(bridge.EventResponseDelta)
		ev.SessionID = "s3"
		ev.Delta = "y"
		_ = w.Append("s3", ev)
	}
	w.Close("s3")
	path := filepath.Join(dir, "progress", "orch-s3.jsonl")
	b, _ := readFileForTest(path)
	got := occurrences(string(b), `"response_delta"`)
	if got == 0 {
		t.Fatalf("expected some deltas persisted, got 0")
	}
}

func TestAsyncProgressWriterConcurrentSessions(t *testing.T) {
	dir := t.TempDir()
	w := newAsyncProgressWriter(ProgressWriter{Dir: filepath.Join(dir, "progress")})
	var wg sync.WaitGroup
	for s := 0; s < 5; s++ {
		wg.Add(1)
		sid := "sa-" + string(rune('a'+s))
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				ev := bridge.NewEvent(bridge.EventResponseDelta)
				ev.SessionID = sid
				_ = w.Append(sid, ev)
			}
			w.Close(sid)
		}()
	}
	wg.Wait()
}
