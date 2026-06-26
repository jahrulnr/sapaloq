package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// threadRecord maps a SapaLOQ SessionID to the Codex thread_id used for
// `codex exec resume`. cwd/codexHome are recorded so a resume targets the same
// on-disk session directory the original turn created (resume is bound to
// CODEX_HOME + the session files there; see CODEX_CLI_CONTRACT.md §2.1).
type threadRecord struct {
	SessionID string    `json:"session_id"`
	ThreadID  string    `json:"thread_id"`
	Cwd       string    `json:"cwd,omitempty"`
	CodexHome string    `json:"codex_home,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// threadStore is an append-only, last-write-wins SessionID->thread_id store
// backed by a JSONL file under the vault dir (sibling of tool-calls.jsonl,
// mirroring how the cursor bridge reuses the vault directory). An in-memory map
// fronts the file for the process lifetime so a lookup never touches disk after
// load.
//
// We use a dedicated tiny store rather than vault.Writer because the vault
// Entry schema is fixed for tool-call audit records; the thread mapping needs
// its own fields (thread_id/cwd/codex_home).
type threadStore struct {
	path string
	mu   sync.Mutex
	m    map[string]threadRecord
}

// newThreadStore loads any existing records from path (last write wins per
// SessionID). A missing file is not an error - the store starts empty.
func newThreadStore(path string) (*threadStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	s := &threadStore{path: path, m: map[string]threadRecord{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// load replays the JSONL file into the in-memory map. Malformed lines are
// skipped tolerantly so a partial/corrupt tail never blocks startup.
func (s *threadStore) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec threadRecord
		if json.Unmarshal(line, &rec) != nil || rec.SessionID == "" {
			continue // tolerant: skip junk/partial lines
		}
		s.m[rec.SessionID] = rec // last write wins
	}
	return sc.Err()
}

// Lookup returns the recorded thread_id for a SessionID, if any.
func (s *threadStore) Lookup(sessionID string) (threadRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.m[sessionID]
	return rec, ok
}

// Save records (or overwrites) the SessionID->thread_id mapping, appending a
// JSON line and updating the in-memory map. A write error is returned but the
// in-memory map is updated regardless so the running process stays consistent
// even if the disk append fails (continuity within the process survives).
func (s *threadStore) Save(rec threadRecord) error {
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	s.m[rec.SessionID] = rec
	path := s.path
	s.mu.Unlock()

	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}
