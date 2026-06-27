package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

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

// asyncProgressBuffered is the size of the per-session delta buffer before the
// async writer starts dropping deltas (it never drops terminal events). Large
// enough to absorb a fast token stream; small enough to bound memory.
const asyncProgressBuffered = 256

// isTerminalProgressEvent reports whether a stream event must be persisted
// synchronously (it marks a durable state transition the user/audit cannot
// lose): done, error, tool_call, task_update, checkpoint. Delta/thinking/
// status events are high-frequency and safe to persist asynchronously.
func isTerminalProgressEvent(ev bridge.StreamEvent) bool {
	switch ev.Kind {
	case bridge.EventDone, bridge.EventError, bridge.EventToolCall, bridge.EventToolUpdate,
		bridge.EventTaskUpdate, bridge.EventCheckpoint, bridge.EventDecisionUpdate,
		bridge.EventSteeringUpdate:
		return true
	default:
		return false
	}
}

// asyncProgressWriter wraps a ProgressWriter with a per-session buffered
// goroutine so the high-frequency delta/thinking stream is persisted off the
// hot path (one syscall per Append today is the main backpressure source on a
// fast token stream). Terminal events (done/error/tool_call/checkpoint/...) are
// flushed synchronously via Flush so the audit log never loses them even if the
// run is cancelled mid-stream.
//
// It is safe for concurrent use across sessions; each session gets its own
// goroutine + channel. Callers that do not want async behavior can keep using
// ProgressWriter directly.
type asyncProgressWriter struct {
	inner ProgressWriter
	mu    sync.Mutex
	closed bool
	// streams maps sessionID -> *sessionStream
	streams map[string]*sessionStream
}

type sessionStream struct {
	ch     chan progressRequest
	done   chan struct{}
	closed bool
}

type progressRequest struct {
	event *bridge.StreamEvent
	ack   chan struct{}
}

func newAsyncProgressWriter(inner ProgressWriter) *asyncProgressWriter {
	return &asyncProgressWriter{inner: inner, streams: make(map[string]*sessionStream)}
}

// Append routes the event: terminal events are written synchronously AND
// forwarded to the async drain so ordering is preserved within a session;
// delta/thinking/status events are enqueued for async drain. A full buffer
// drops the delta (best-effort progress log) but never blocks the stream.
func (a *asyncProgressWriter) Append(sessionID string, ev bridge.StreamEvent) error {
	if a == nil || a.inner.Dir == "" || sessionID == "" {
		return nil
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	s, ok := a.streams[sessionID]
	if !ok {
		s = a.startLocked(sessionID)
	}
	if isTerminalProgressEvent(ev) {
		ack := make(chan struct{})
		// Holding a.mu while enqueueing makes Close unable to close this channel
		// between lookup and send. The dedicated writer is the only consumer, so
		// FIFO ordering is preserved and the terminal ack means it is on disk.
		s.ch <- progressRequest{event: &ev, ack: ack}
		a.mu.Unlock()
		<-ack
		return nil
	}
	select {
	case s.ch <- progressRequest{event: &ev}:
	default:
		// Buffer full: drop only high-frequency non-terminal progress.
	}
	a.mu.Unlock()
	return nil
}

// startLocked launches the per-session drain goroutine. Caller holds a.mu.
func (a *asyncProgressWriter) startLocked(sessionID string) *sessionStream {
	s := &sessionStream{
		ch:   make(chan progressRequest, asyncProgressBuffered),
		done: make(chan struct{}),
	}
	a.streams[sessionID] = s
	go func() {
		defer close(s.done)
		for req := range s.ch {
			if req.event != nil {
				_ = a.inner.Append(sessionID, *req.event)
			}
			if req.ack != nil {
				close(req.ack)
			}
		}
	}()
	return s
}

// flushSync drains any buffered async events for a session so a subsequent
// terminal write lands after them in the JSONL. It does NOT close the stream
// (more deltas may arrive on the next turn).
func (a *asyncProgressWriter) flushSync(sessionID string) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	s, ok := a.streams[sessionID]
	if !ok {
		a.mu.Unlock()
		return
	}
	ack := make(chan struct{})
	s.ch <- progressRequest{ack: ack}
	a.mu.Unlock()
	<-ack
}

// Close terminates the async drain for a session and flushes remaining events.
// Safe to call multiple times. Used on run end so the goroutine does not leak.
func (a *asyncProgressWriter) Close(sessionID string) {
	a.mu.Lock()
	s, ok := a.streams[sessionID]
	if !ok || s.closed {
		a.mu.Unlock()
		return
	}
	s.closed = true
	delete(a.streams, sessionID)
	a.mu.Unlock()
	close(s.ch)
	<-s.done
}

func (a *asyncProgressWriter) CloseAll() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.closed = true
	streams := a.streams
	a.streams = make(map[string]*sessionStream)
	for _, s := range streams {
		s.closed = true
		close(s.ch)
	}
	a.mu.Unlock()
	for _, s := range streams {
		<-s.done
	}
}
