package wire

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// StreamIdleWatch cancels a stream when no activity occurs for idle duration.
// Each Reset() refills the countdown; Pause/Resume hold the timer during local
// MCP/exec work that produces no upstream frames.
type StreamIdleWatch struct {
	mu       sync.Mutex
	timer    *time.Timer
	idle     time.Duration
	cancel   context.CancelFunc
	paused   int
	timedOut bool
}

func NewStreamIdleWatch(cancel context.CancelFunc, idle time.Duration) *StreamIdleWatch {
	if cancel == nil || idle <= 0 {
		return nil
	}
	w := &StreamIdleWatch{cancel: cancel, idle: idle}
	w.timer = time.AfterFunc(idle, w.fire)
	return w
}

func (w *StreamIdleWatch) fire() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.paused > 0 {
		w.mu.Unlock()
		return
	}
	w.timedOut = true
	w.mu.Unlock()
	w.cancel()
}

func (w *StreamIdleWatch) Reset() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.paused > 0 || w.timer == nil {
		return
	}
	w.timer.Reset(w.idle)
}

func (w *StreamIdleWatch) Pause() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.paused++
	if w.timer != nil {
		w.timer.Stop()
	}
}

func (w *StreamIdleWatch) Resume() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.paused > 0 {
		w.paused--
	}
	if w.paused > 0 || w.timer == nil {
		return
	}
	w.timer.Reset(w.idle)
}

func (w *StreamIdleWatch) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

func (w *StreamIdleWatch) TimedOut() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.timedOut
}

func agentStreamIdleErr(watch *StreamIdleWatch, idle time.Duration) error {
	if watch != nil && watch.TimedOut() && idle > 0 {
		return fmt.Errorf("agent stream idle: no activity for %s", idle)
	}
	return nil
}
