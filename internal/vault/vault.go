package vault

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jahrulnr/sapaloq/internal/privacyfilter"
)

// Entry records a provider tool call. It serves two purposes:
//   - reason="undeclared": a provider (e.g. cursor, whose tool surface is
//     hardcoded server-side) called a tool outside the companion declared
//     surface - the original anomaly/audit signal.
//   - reason="executed": the orchestrator's audit trail of every tool it ran
//     (added later). Both share this log, distinguished by Reason.
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

// Default rotation policy. The log is an append-only JSON-lines file; without
// rotation it would grow unbounded (and ReadEntries, which slurps the whole
// file, would get ever slower). When the primary file would exceed MaxBytes it
// is rotated to a numbered sibling (.1, .2, …) and the oldest beyond KeepFiles
// is dropped.
const (
	DefaultMaxBytes  int64 = 5 << 20 // 5 MiB
	DefaultKeepFiles       = 3
)

// Options tunes the rotating writer. Zero/negative values fall back to the
// defaults, so the zero Options is the safe default policy.
type Options struct {
	// MaxBytes is the size at/after which the primary log rotates. <=0 → default.
	MaxBytes int64
	// KeepFiles is how many rotated siblings (.1 … .N) to retain. <=0 → default.
	KeepFiles int
}

func (o Options) withDefaults() Options {
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.KeepFiles <= 0 {
		o.KeepFiles = DefaultKeepFiles
	}
	return o
}

type Writer struct {
	path     string
	opts     Options
	redactor *privacyfilter.Filter
	mu       sync.Mutex
}

// New constructs a Writer with the default rotation policy (5 MiB / keep 3).
// Existing callers keep working unchanged.
func New(path string) (*Writer, error) {
	return NewWithOptions(path, Options{})
}

// NewWithOptions constructs a Writer with an explicit rotation policy. A zero
// Options yields the defaults.
func NewWithOptions(path string, opts Options) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &Writer{path: path, opts: opts.withDefaults(), redactor: privacyfilter.New()}, nil
}

// Append writes one entry as a JSON line, rotating first when the primary file
// would exceed the configured MaxBytes. Rotation is best-effort: if any rename
// fails we fall back to a plain append so an audit write is never lost.
func (w *Writer) Append(entry Entry) error {
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	entry.Arguments = sanitizeArguments(w.redactor, entry.Arguments)
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	b = append(b, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	// Rotate when the current size + this line would exceed the cap. A brand
	// new (absent) file never rotates; a single oversize line still gets
	// written (we never split a line) but triggers rotation next time.
	if fi, statErr := os.Stat(w.path); statErr == nil {
		if fi.Size() > 0 && fi.Size()+int64(len(b)) > w.opts.MaxBytes {
			w.rotate() // best-effort; ignore error and append regardless
		}
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}

const maxAuditArgumentsBytes = 16 * 1024

func sanitizeArguments(redactor *privacyfilter.Filter, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxAuditArgumentsBytes {
		sum := sha256.Sum256(raw)
		b, _ := json.Marshal(map[string]any{
			"omitted": true,
			"bytes":   len(raw),
			"sha256":  fmt.Sprintf("%x", sum[:]),
		})
		return b
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		text := string(raw)
		if redactor != nil {
			text = redactor.Redact(text).Redacted
		}
		b, _ := json.Marshal(map[string]any{"invalid_json": true, "redacted": text})
		return b
	}
	value = redactAuditValue(redactor, value)
	b, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{"omitted":true}`)
	}
	return b
}

func redactAuditValue(redactor *privacyfilter.Filter, value any) any {
	switch typed := value.(type) {
	case string:
		if redactor != nil {
			typed = redactor.Redact(typed).Redacted
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = redactAuditValue(redactor, typed[i])
		}
		return typed
	case map[string]any:
		for key, child := range typed {
			typed[key] = redactAuditValue(redactor, child)
		}
		return typed
	default:
		return value
	}
}

// rotate cascades the numbered siblings: drop the oldest (>KeepFiles), shift
// .N→.N+1 down to .1, then move the primary to .1. Errors are returned but the
// caller treats rotation as best-effort.
func (w *Writer) rotate() error {
	keep := w.opts.KeepFiles
	// Remove the file that would fall off the end (e.g. .3 when keep=3).
	oldest := fmt.Sprintf("%s.%d", w.path, keep)
	_ = os.Remove(oldest)
	// Shift .{n} → .{n+1} for n = keep-1 down to 1.
	for n := keep - 1; n >= 1; n-- {
		src := fmt.Sprintf("%s.%d", w.path, n)
		dst := fmt.Sprintf("%s.%d", w.path, n+1)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		}
	}
	// Move the primary to .1.
	return os.Rename(w.path, w.path+".1")
}
