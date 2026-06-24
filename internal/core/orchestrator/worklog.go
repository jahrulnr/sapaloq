package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// workerLogError appends a single timestamped line to a worker's error-only log
// at memory/workers/<task-id>/error.log. This is deliberately separate from the
// verbose progress JSONL: when something goes wrong, you want a short,
// chronological, errors-only trail per agent - not to grep a firehose.
//
// It is best-effort and gated by the completion.workerErrorLog config flag:
// a nil orchestrator, empty workersDir, or write error is silently ignored so
// logging never disrupts the task itself.
func (o *Orchestrator) workerLogError(taskID, msg string) {
	if o == nil || taskID == "" || msg == "" {
		return
	}
	if o.workersDir == "" {
		return
	}
	if !o.cfg.Orchestrator.WithDefaults().Completion.WorkerErrorLog {
		return
	}
	dir := filepath.Join(o.workersDir, filepath.Base(taskID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "error.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line := fmt.Sprintf("%s %s\n", time.Now().UTC().Format(time.RFC3339), msg)
	_, _ = f.WriteString(line)
}
