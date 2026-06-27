package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func dirsForData(dataDir string) RuntimeDirsInfo {
	return RuntimeDirs(Config{Runtime: RuntimeConfig{DataDir: dataDir}})
}

func TestRuntimeDirsLayout(t *testing.T) {
	data := t.TempDir()
	dirs := dirsForData(data)

	if got, want := dirs.MemoryDir, filepath.Join(data, "memory"); got != want {
		t.Errorf("MemoryDir = %q, want %q", got, want)
	}
	if got, want := dirs.StateDir, filepath.Join(data, "state"); got != want {
		t.Errorf("StateDir = %q, want %q", got, want)
	}
	for name, got := range map[string]string{
		"TasksDir":    dirs.TasksDir,
		"ProgressDir": dirs.ProgressDir,
		"WorkersDir":  dirs.WorkersDir,
	} {
		if filepath.Dir(got) != dirs.StateDir {
			t.Errorf("%s = %q, expected to live under StateDir %q", name, got, dirs.StateDir)
		}
	}

	if err := EnsureRuntimeDirs(dirs); err != nil {
		t.Fatalf("EnsureRuntimeDirs: %v", err)
	}
	for _, d := range []string{dirs.MemoryDir, dirs.StateDir, dirs.TasksDir, dirs.ProgressDir, dirs.WorkersDir} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("expected dir %q to exist: err=%v", d, err)
		}
	}
}

func TestMigrateLegacyLayout(t *testing.T) {
	data := t.TempDir()
	dirs := dirsForData(data)

	// Seed a legacy memory/ layout: a task record, worker health, a progress
	// stream, the event WAL, plus companion.db which must be left in place.
	mustWrite(t, filepath.Join(dirs.MemoryDir, "tasks", "task-1", "status.json"), `{"id":"task-1"}`)
	mustWrite(t, filepath.Join(dirs.MemoryDir, "workers", "task-1", "health.json"), `{"id":"task-1"}`)
	mustWrite(t, filepath.Join(dirs.MemoryDir, "progress", "chat-1.jsonl"), `{"kind":"status"}`)
	mustWrite(t, filepath.Join(dirs.MemoryDir, "events.jsonl"), `{"seq":1}`)
	mustWrite(t, filepath.Join(dirs.MemoryDir, "companion.db"), "SQLITE")

	if err := MigrateLegacyLayout(dirs); err != nil {
		t.Fatalf("MigrateLegacyLayout: %v", err)
	}

	// Moved into state/.
	assertFile(t, filepath.Join(dirs.TasksDir, "task-1", "status.json"), `{"id":"task-1"}`)
	assertFile(t, filepath.Join(dirs.WorkersDir, "task-1", "health.json"), `{"id":"task-1"}`)
	assertFile(t, filepath.Join(dirs.ProgressDir, "chat-1.jsonl"), `{"kind":"status"}`)
	assertFile(t, filepath.Join(dirs.StateDir, "events.jsonl"), `{"seq":1}`)

	// Originals removed.
	for _, p := range []string{
		filepath.Join(dirs.MemoryDir, "tasks"),
		filepath.Join(dirs.MemoryDir, "workers"),
		filepath.Join(dirs.MemoryDir, "progress"),
		filepath.Join(dirs.MemoryDir, "events.jsonl"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected legacy path %q to be gone, err=%v", p, err)
		}
	}

	// companion.db must remain untouched in memory/.
	assertFile(t, filepath.Join(dirs.MemoryDir, "companion.db"), "SQLITE")
}

func TestMigrateLegacyLayoutIdempotentAndNonClobbering(t *testing.T) {
	data := t.TempDir()
	dirs := dirsForData(data)

	// A fresh state/ already holds the current task record; a stale legacy copy
	// exists too. The migration must NOT clobber the new one.
	mustWrite(t, filepath.Join(dirs.TasksDir, "task-1", "status.json"), `{"id":"new"}`)
	mustWrite(t, filepath.Join(dirs.MemoryDir, "tasks", "task-1", "status.json"), `{"id":"stale"}`)

	if err := MigrateLegacyLayout(dirs); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	assertFile(t, filepath.Join(dirs.TasksDir, "task-1", "status.json"), `{"id":"new"}`)

	// Running again is a no-op and must not error.
	if err := MigrateLegacyLayout(dirs); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	assertFile(t, filepath.Join(dirs.TasksDir, "task-1", "status.json"), `{"id":"new"}`)
}

func TestMigrateDefaultDataRootKeepsConfigAndMovesRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldRoot := filepath.Join(home, ".config", "sapaloq")
	newRoot := filepath.Join(home, "SapaLOQ")
	mustWrite(t, filepath.Join(oldRoot, "config.json"), `{"keep":true}`)
	mustWrite(t, filepath.Join(oldRoot, ".env"), "TOKEN=secret")
	mustWrite(t, filepath.Join(oldRoot, "memory", "companion.db"), "db")
	mustWrite(t, filepath.Join(oldRoot, "prompts", "ask.md"), "prompt")

	if err := MigrateDefaultDataRoot(); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(oldRoot, "config.json"), `{"keep":true}`)
	assertFile(t, filepath.Join(oldRoot, ".env"), "TOKEN=secret")
	assertFile(t, filepath.Join(newRoot, "memory", "companion.db"), "db")
	assertFile(t, filepath.Join(newRoot, "prompts", "ask.md"), "prompt")
}

func TestMigrateDefaultDataRootDoesNotClobberDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldRoot := filepath.Join(home, ".config", "sapaloq")
	newRoot := filepath.Join(home, "SapaLOQ")
	mustWrite(t, filepath.Join(oldRoot, "memory", "companion.db"), "old")
	mustWrite(t, filepath.Join(newRoot, "memory", "companion.db"), "new")

	if err := MigrateDefaultDataRoot(); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(newRoot, "memory", "companion.db"), "new")
	assertFile(t, filepath.Join(newRoot, "migration-archive", "memory", "companion.db"), "old")
	if _, err := os.Stat(filepath.Join(oldRoot, "memory")); !os.IsNotExist(err) {
		t.Fatalf("conflicting legacy memory should be archived, err=%v", err)
	}
}

func TestMigrateLegacyLayoutRestoresMisplacedDurableMemory(t *testing.T) {
	data := t.TempDir()
	dirs := dirsForData(data)
	mustWrite(t, filepath.Join(dirs.StateDir, "memory", "facts.json"), `[{"id":1}]`)

	if err := MigrateLegacyLayout(dirs); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dirs.MemoryDir, "facts.json"), `[{"id":1}]`)
	if _, err := os.Stat(filepath.Join(dirs.StateDir, "memory")); !os.IsNotExist(err) {
		t.Fatalf("misplaced memory directory should be removed, err=%v", err)
	}
}

func TestMigrateLegacyLayoutPrefersNewerMisplacedMemoryFile(t *testing.T) {
	data := t.TempDir()
	dirs := dirsForData(data)
	dst := filepath.Join(dirs.MemoryDir, "facts.json")
	src := filepath.Join(dirs.StateDir, "memory", "facts.json")
	mustWrite(t, dst, `[{"id":"old"}]`)
	mustWrite(t, src, `[{"id":"new"}]`)
	_ = os.Chtimes(dst, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	_ = os.Chtimes(src, time.Now(), time.Now())

	if err := MigrateLegacyLayout(dirs); err != nil {
		t.Fatal(err)
	}
	assertFile(t, dst, `[{"id":"new"}]`)
	if _, err := os.Stat(filepath.Join(dirs.StateDir, "memory")); !os.IsNotExist(err) {
		t.Fatalf("misplaced memory directory should be removed, err=%v", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("file %q = %q, want %q", path, string(got), want)
	}
}
