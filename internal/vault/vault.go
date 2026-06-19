package vault

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry records a provider tool call that is not on the companion declared surface.
type Entry struct {
	At           time.Time       `json:"at"`
	SessionID    string          `json:"session_id,omitempty"`
	Provider     string          `json:"provider"`
	RawName      string          `json:"raw_name"`
	ResolvedName string          `json:"resolved_name"`
	Arguments    json.RawMessage `json:"arguments,omitempty"`
	Source       string          `json:"source,omitempty"`
	Reason       string          `json:"reason"`
}

type Writer struct {
	path string
	mu   sync.Mutex
}

func New(path string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &Writer{path: path}, nil
}

func (w *Writer) Append(entry Entry) error {
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}
