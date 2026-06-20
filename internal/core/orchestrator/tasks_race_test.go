package orchestrator

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestReadTaskNoJSONRace hammers writeTask/readTask concurrently to prove the
// atomic write fix prevents the "unexpected end of JSON input" error that
// previously broke sapaloq_wait (Bug #2). Before the fix, os.WriteFile could
// truncate status.json while a reader was mid-read.
func TestReadTaskNoJSONRace(t *testing.T) {
	o := &Orchestrator{memoryDir: t.TempDir()}
	id := "task-race-test"
	base := taskRecord{
		ID:        id,
		Role:      "planner",
		Status:    "pending",
		Task:      "race",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := o.writeTask(base); err != nil {
		t.Fatalf("seed writeTask: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: rapidly rewrites status.json with changing statuses.
	wg.Add(1)
	go func() {
		defer wg.Done()
		statuses := []string{"pending", "in_progress", "done", "failed"}
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			rec := base
			rec.Status = statuses[i%len(statuses)]
			rec.UpdatedAt = time.Now().UTC()
			if err := o.writeTask(rec); err != nil {
				t.Errorf("writeTask: %v", err)
				return
			}
			i++
		}
	}()

	// Readers: continuously read; must never see a JSON parse error.
	const readers = 8
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				if _, err := o.readTask(id); err != nil {
					t.Errorf("readTask returned error during concurrent write: %v", err)
					return
				}
			}
		}()
	}

	// Let readers finish, then stop the writer.
	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestWriteFileAtomic verifies the helper writes correct content and perms and
// never leaves a partial file in place.
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	want := []byte(`{"id":"x"}` + "\n")
	if err := writeFileAtomic(path, want, 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("content mismatch: got %q want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", info.Mode().Perm())
	}
	// No stray temp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "status.json" {
			t.Fatalf("unexpected leftover file: %s", e.Name())
		}
	}
}
