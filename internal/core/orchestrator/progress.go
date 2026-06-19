package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

type ProgressWriter struct {
	Dir string
}

func (w ProgressWriter) Append(sessionID string, ev bridge.StreamEvent) error {
	if w.Dir == "" || sessionID == "" {
		return nil
	}
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(w.Dir, "orch-"+sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}
