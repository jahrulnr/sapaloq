package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WorkerHandle is the live, observable identity of one running sub-agent
// (planner / task-runner / scribe / ask-delegated). It answers the question the
// orchestrator previously could not: "is this agent healthy, or has it
// silently wedged?" — by exposing a PID, a phase, and a heartbeat the watchdog
// monitors.
//
// PID is os.Getpid() today (every in-process worker shares the core's PID).
// The field is deliberately first-class so a future upgrade to real subprocess
// workers (via internal/node Transport) only has to populate a distinct PID —
// no consumer or schema change required.
type WorkerHandle struct {
	ID            string    `json:"id"`
	Role          string    `json:"role"`
	SessionID     string    `json:"session_id,omitempty"`
	Node          string    `json:"node,omitempty"`
	PID           int       `json:"pid"`
	Phase         string    `json:"phase"`
	Status        string    `json:"status"`
	StartedAt     time.Time `json:"started_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

// healthy reports whether the worker has produced a heartbeat within staleAfter.
func (h WorkerHandle) healthy(now time.Time, staleAfter time.Duration) bool {
	return now.Sub(h.LastHeartbeat) <= staleAfter
}

// workerRegistry is the orchestrator's in-process roster of live workers. It is
// the single source of truth for liveness/health, separate from the durable
// status.json (which records outcome, not heartbeat).
type workerRegistry struct {
	mu      sync.Mutex
	workers map[string]*WorkerHandle
	dir     string // memory/workers; "" disables snapshot persistence
}

func newWorkerRegistry(dir string) *workerRegistry {
	return &workerRegistry{workers: make(map[string]*WorkerHandle), dir: dir}
}

// register adds (or replaces) a worker and records its first heartbeat. Called
// at the start of runBackgroundTask.
func (r *workerRegistry) register(id, role, sessionID, node string) {
	if r == nil || id == "" {
		return
	}
	now := time.Now().UTC()
	h := &WorkerHandle{
		ID:            id,
		Role:          role,
		SessionID:     sessionID,
		Node:          node,
		PID:           os.Getpid(),
		Phase:         "starting",
		Status:        "in_progress",
		StartedAt:     now,
		LastHeartbeat: now,
	}
	r.mu.Lock()
	r.workers[id] = h
	snap := *h
	r.mu.Unlock()
	r.persist(snap)
}

// heartbeat refreshes liveness and (optionally) the current phase. Called every
// turn of the sub-agent loop and on each tool activity, so a worker that is
// genuinely doing work never looks stalled.
func (r *workerRegistry) heartbeat(id, phase string) {
	if r == nil || id == "" {
		return
	}
	r.mu.Lock()
	h := r.workers[id]
	if h == nil {
		r.mu.Unlock()
		return
	}
	h.LastHeartbeat = time.Now().UTC()
	if phase != "" {
		h.Phase = phase
	}
	snap := *h
	r.mu.Unlock()
	r.persist(snap)
}

// setPhase updates only the human-readable phase label for observability. It
// does NOT advance the heartbeat — liveness is owned by the structural ticker
// in runBackgroundTask so a long but legitimate operation (a slow tool, slow
// stream) never looks stalled. No-op for unknown ids.
func (r *workerRegistry) setPhase(id, phase string) {
	if r == nil || id == "" || phase == "" {
		return
	}
	r.mu.Lock()
	h := r.workers[id]
	if h == nil {
		r.mu.Unlock()
		return
	}
	h.Phase = phase
	snap := *h
	r.mu.Unlock()
	r.persist(snap)
}

// deregister removes a worker once its goroutine exits, recording the final
// status in the persisted snapshot for post-mortem inspection.
func (r *workerRegistry) deregister(id, finalStatus string) {
	if r == nil || id == "" {
		return
	}
	r.mu.Lock()
	h := r.workers[id]
	if h != nil {
		h.Status = finalStatus
		h.Phase = "exited"
		h.LastHeartbeat = time.Now().UTC()
		delete(r.workers, id)
	}
	var snap WorkerHandle
	if h != nil {
		snap = *h
	}
	r.mu.Unlock()
	if h != nil {
		r.persist(snap)
	}
}

// snapshot returns a copy of every live worker, for observability/debugging.
func (r *workerRegistry) snapshot() []WorkerHandle {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]WorkerHandle, 0, len(r.workers))
	for _, h := range r.workers {
		out = append(out, *h)
	}
	return out
}

// stalled returns the live workers whose last heartbeat is older than
// staleAfter — i.e. wedged goroutines masquerading as in_progress.
func (r *workerRegistry) stalled(staleAfter time.Duration) []WorkerHandle {
	if r == nil {
		return nil
	}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []WorkerHandle
	for _, h := range r.workers {
		if !h.healthy(now, staleAfter) {
			out = append(out, *h)
		}
	}
	return out
}

// persist writes a per-worker health snapshot to memory/workers/<id>/health.json
// so liveness survives inspection from outside the process. Best-effort.
func (r *workerRegistry) persist(h WorkerHandle) {
	if r == nil || r.dir == "" || h.ID == "" {
		return
	}
	dir := filepath.Join(r.dir, filepath.Base(h.ID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return
	}
	_ = writeFileAtomic(filepath.Join(dir, "health.json"), append(raw, '\n'), 0o600)
}

// startWorkerWatchdog runs a periodic health sweep: any live worker that has
// not heartbeat within staleAfter is force-failed with an explicit reason and
// its cancel func invoked, so a wedged worker can no longer sit at
// "in_progress" forever (the core of the "we never know if it finished" bug).
// It is a no-op when the registry is nil.
func (o *Orchestrator) StartWorkerWatchdog(ctx context.Context) {
	if o == nil || o.workers == nil {
		return
	}
	cc := o.cfg.Orchestrator.WithDefaults().Completion
	interval := time.Duration(cc.HeartbeatIntervalSec) * time.Second
	stale := time.Duration(cc.StaleAfterSec) * time.Second
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				o.sweepStalledWorkers(stale)
			}
		}
	}()
}

// sweepStalledWorkers fails every worker past the stale window. Exposed
// (unexported) for direct testing without spinning the ticker.
func (o *Orchestrator) sweepStalledWorkers(stale time.Duration) {
	for _, h := range o.workers.stalled(stale) {
		reason := "worker stalled: no heartbeat within the health window"
		o.workerLogError(h.ID, reason)
		// Cancel the wedged goroutine (best-effort) so it stops consuming a
		// slot, then mark the durable record failed and publish it.
		o.taskMu.Lock()
		cancel := o.taskCancels[h.ID]
		o.taskMu.Unlock()
		if cancel != nil {
			cancel()
		}
		record, err := o.readTask(h.ID)
		if err != nil {
			o.workers.deregister(h.ID, "failed")
			continue
		}
		if taskTerminal(record.Status) {
			// Already finished between the sweep read and now; nothing to do.
			o.workers.deregister(h.ID, record.Status)
			continue
		}
		record.Status = "failed"
		record.Error = reason
		record.UpdatedAt = time.Now().UTC()
		_ = o.writeTask(record)
		o.publishTaskUpdate(record.SessionID, record)
		o.workers.deregister(h.ID, "failed")
	}
}
