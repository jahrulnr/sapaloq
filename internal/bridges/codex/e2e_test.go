//go:build e2e

// E2E tests for the codex-bridge spawn the REAL codex CLI. They are gated
// behind the `e2e` build tag so a plain `go test ./...` never runs them (CI
// stays green offline). Even with the tag set, each test skips automatically if
// the codex binary cannot be resolved on PATH, so the suite is safe on hosts
// without the CLI.
//
// Run them with:
//
//	go test -tags=e2e ./internal/bridges/codex/ -run TestE2E -v
//	go test -tags=e2e ./internal/bridges/codex/ -run TestGenerate -update -v   # regenerate fixtures
package codex

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// update, when set, makes TestGenerateFixtures overwrite the golden fixture
// files in testdata/ with the raw JSONL captured from the real CLI.
var update = flag.Bool("update", false, "regenerate golden fixtures from a real codex run")

// requireCodex skips the test unless the codex binary is resolvable on PATH.
func requireCodex(t *testing.T) string {
	t.Helper()
	bin, err := resolveBinary()
	if err != nil {
		t.Skipf("codex binary not found on PATH; skipping e2e: %v", err)
	}
	return bin
}

// TestE2EPong runs a real, read-only turn and asserts the visible answer comes
// back as a response delta followed by a done terminal — the live counterpart
// of the PONG fixture test.
func TestE2EPong(t *testing.T) {
	bin := requireCodex(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// runCodex blocks until the stream drains and writes events synchronously,
	// so drain in a goroutine to avoid backpressure on the channel.
	out := make(chan bridge.StreamEvent, 64)
	type collected struct {
		response string
		sawErr   string
	}
	done := make(chan collected, 1)
	go func() {
		var c collected
		for ev := range out {
			switch ev.Kind {
			case bridge.EventResponseDelta:
				c.response += ev.Delta
			case bridge.EventError:
				c.sawErr = ev.Error
			}
		}
		done <- c
	}()

	res, err := runCodex(ctx, runOptions{
		binary:  bin,
		prompt:  "Reply with exactly the word PONG and nothing else.",
		sandbox: "read-only",
		env:     os.Environ(),
	}, "e2e-pong", out)
	close(out)
	c := <-done
	response, sawErr := c.response, c.sawErr
	if err != nil {
		t.Fatalf("runCodex: %v", err)
	}
	if sawErr != "" {
		t.Fatalf("real turn errored: %s", sawErr)
	}
	if response == "" {
		t.Fatalf("expected a non-empty agent response")
	}
	if res.threadID == "" {
		t.Fatalf("expected a captured thread_id from thread.started")
	}
	t.Logf("real PONG response: %q (thread=%s)", response, res.threadID)
}

// TestGenerateFixtures captures the raw JSONL of a real read-only turn and,
// with -update, writes it to testdata/pong.jsonl. This is the "real run ->
// capture -> golden file" half; the offline tests replay the same golden file.
func TestGenerateFixtures(t *testing.T) {
	if !*update {
		t.Skip("pass -update to regenerate golden fixtures from a real codex run")
	}
	bin := requireCodex(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var raw bytes.Buffer
	out := make(chan bridge.StreamEvent, 64)
	drained := make(chan struct{})
	go func() {
		for range out {
		}
		close(drained)
	}()
	_, err := runCodex(ctx, runOptions{
		binary:  bin,
		prompt:  "Reply with exactly the word PONG and nothing else.",
		sandbox: "read-only",
		env:     os.Environ(),
		rawSink: &raw,
	}, "e2e-gen", out)
	close(out)
	<-drained
	if err != nil {
		t.Fatalf("runCodex: %v", err)
	}
	if raw.Len() == 0 {
		t.Fatalf("captured no JSONL from the real run")
	}

	path := filepath.Join("testdata", "pong.jsonl")
	if err := os.WriteFile(path, raw.Bytes(), 0o644); err != nil {
		t.Fatalf("write fixture %q: %v", path, err)
	}
	t.Logf("regenerated %s (%d bytes):\n%s", path, raw.Len(), raw.String())
}
