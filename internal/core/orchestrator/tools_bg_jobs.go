package orchestrator

// tools_bg_jobs.go is the generic background-job registry. It replaces the old
// exec-only asyncExecRegistry with one registry that runs an arbitrary
// `func(ctx) (string, error)` so ANY tool can be fired-and-forgotten via its
// `wait_for_output: false` argument and later collected via the unified `wait`
// tool (mode=tool). The shell-specific concerns (process-group kill, CWD
// persistence) live in a small adapter produced by execBgRun; the registry
// itself is tool-agnostic.
//
//   - spawn(toolName, run): enqueue a job, return a live handle + job_id.
//   - wait(id, timeout): block until the job is terminal or the timeout fires.
//   - snapshot(id): cheap status read (in-memory, falls back to disk).
//   - cancel(id): cancel a running job and return the final snapshot.
//   - recover(): on core start, mark in-flight jobs failed.
//   - GC + cap: keep the in-memory map bounded; on-disk records survive longer.
//
// Jobs persist to state/tool-jobs/<id>.json so a crashed core can recover /
// list them on the next boot. The on-disk schema is intentionally compatible
// with toolJobScheduler (same Status enum) so the recovery path can reason
// about both kinds of job with one code path.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// bgJobStatus mirrors toolJobStatus. The values are persisted as strings
// (queued/running/completed/failed/cancelled) so the same JSON can be read
// from disk during recovery and by other orchestrator tooling.
type bgJobStatus string

const (
	bgJobQueued    bgJobStatus = "queued"
	bgJobRunning   bgJobStatus = "running"
	bgJobCompleted bgJobStatus = "completed"
	bgJobFailed    bgJobStatus = "failed"
	bgJobCancelled bgJobStatus = "cancelled"
)

// bgJobRun is the work a background job executes. It receives a context that
// is cancelled when the host cancels the job (via sapaloq_cancel_job) or when
// the core shuts down. The returned string is the tool result text the model
// later collects via `wait mode:tool`; the error, when non-nil, marks the job
// failed and is surfaced as the job's `error` field.
type bgJobRun func(ctx context.Context) (string, error)

// bgJob is the durable + live state of one background tool invocation.
// The on-disk representation is bgJobSnapshot (a struct without the in-process
// fields like mu, Cancel, Done, run) so a JSON round-trip never serializes a
// mutex or a func. Live callers should hold *bgJob.
type bgJob struct {
	ID          string      `json:"id"`
	ToolName    string      `json:"tool_name"`
	RunID       string      `json:"run_id,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	Status      bgJobStatus `json:"status"`
	Output      string      `json:"output,omitempty"`
	Error       string      `json:"error,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	StartedAt   *time.Time  `json:"started_at,omitempty"`
	CompletedAt *time.Time  `json:"completed_at,omitempty"`
	// run is the work to execute; populated only in-process, dropped from JSON.
	run context.CancelFunc `json:"-"`
	// Cancel cancels the job's execution context. Populated only in-process.
	Cancel context.CancelFunc `json:"-"`
	// Done is closed when Status is terminal. Waiters select on this channel
	// instead of busy-waiting. Both run() (natural finish) and cancel() can
	// reach the terminal state, so the close is funnelled through doneOnce.
	Done chan struct{} `json:"-"`
	// doneOnce guards exactly one close(Done) across the run()/cancel() race.
	doneOnce sync.Once `json:"-"`
	// mu guards the live mutable fields (Status, Output, Error, StartedAt,
	// CompletedAt) so concurrent snapshot / wait / cancel calls never see a
	// torn write. The on-disk file is written after releasing mu.
	mu sync.Mutex `json:"-"`
}

// bgJobSnapshot is the JSON-safe view of a job: what gets written to disk and
// what callers pass to bgJobToView. Building one requires holding job.mu and
// then projecting fields by value.
type bgJobSnapshot struct {
	ID          string      `json:"id"`
	ToolName    string      `json:"tool_name"`
	RunID       string      `json:"run_id,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	Status      bgJobStatus `json:"status"`
	Output      string      `json:"output,omitempty"`
	Error       string      `json:"error,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	StartedAt   *time.Time  `json:"started_at,omitempty"`
	CompletedAt *time.Time  `json:"completed_at,omitempty"`
}

// snapshotOf builds the JSON-safe view while holding the live job's mutex.
func (j *bgJob) snapshotOf() bgJobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	return bgJobSnapshot{
		ID:          j.ID,
		ToolName:    j.ToolName,
		RunID:       j.RunID,
		SessionID:   j.SessionID,
		Status:      j.Status,
		Output:      j.Output,
		Error:       j.Error,
		CreatedAt:   j.CreatedAt,
		StartedAt:   j.StartedAt,
		CompletedAt: j.CompletedAt,
	}
}

// closeDone closes the Done channel exactly once, even when run() and cancel()
// both reach a terminal state for the same job (a cancel racing the job's own
// completion). Without this funnel the second close panics.
func (j *bgJob) closeDone() {
	j.doneOnce.Do(func() { close(j.Done) })
}

// terminal reports whether the job has reached a final state.
func (j *bgJob) terminal() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Status.terminal()
}

// bgJobRegistry is the in-process roster of live background jobs. It is the
// authoritative state for in-flight jobs; the JSON files in state/tool-jobs/
// are just snapshots written on every transition.
type bgJobRegistry struct {
	dir string
	mu  sync.Mutex
	seq uint64
	// jobs holds only non-terminal jobs plus recently-completed ones within
	// the retention window. Once a job is GC'd it is still on disk; the
	// in-memory map is just a fast path for active polling.
	jobs map[string]*bgJob
	// cap is the soft cap on in-memory jobs before we evict.
	cap int
	// sem bounds concurrent background execution so fire-and-forget can't
	// over-parallelize. Size = continuation.maxParallelTools (reuse). Nil when
	// the registry is constructed without a cap (tests).
	sem chan struct{}
}

func newBgJobRegistry(dir string, parallel int) *bgJobRegistry {
	r := &bgJobRegistry{
		dir:  dir,
		jobs: make(map[string]*bgJob),
		cap:  256,
	}
	if parallel > 0 {
		r.sem = make(chan struct{}, parallel)
	}
	r.recover()
	return r
}

// root returns the on-disk directory for background job snapshots, even when
// the registry is nil. Used by tests and recovery helpers.
func (r *bgJobRegistry) root() string {
	if r == nil || strings.TrimSpace(r.dir) == "" {
		return ""
	}
	return r.dir
}

// spawn enqueues a new job and starts its background goroutine. Returns the
// live handle (for in-process polling). The goroutine writes its terminal
// state to disk and into the registry under a single mu lock.
func (r *bgJobRegistry) spawn(ctx context.Context, toolName, runID, sessionID string, run bgJobRun) *bgJob {
	if r == nil || run == nil {
		return nil
	}
	now := time.Now().UTC()
	job := &bgJob{
		ID:        fmt.Sprintf("bg-%d-%d", now.UnixNano(), atomic.AddUint64(&r.seq, 1)),
		ToolName:  toolName,
		RunID:     runID,
		SessionID: sessionID,
		Status:    bgJobQueued,
		CreatedAt: now,
		Done:      make(chan struct{}),
	}
	runCtx, cancel := context.WithCancel(context.Background())
	job.Cancel = cancel
	r.mu.Lock()
	r.jobs[job.ID] = job
	r.evictIfNeededLocked()
	r.mu.Unlock()
	r.persist(job)
	go r.execute(runCtx, job, run)
	return job
}

// execute runs the job's work and transitions it to a terminal state. It is
// the only goroutine that mutates the job's status/output once it has left
// "queued". Cancel from elsewhere calls job.Cancel which fires runCtx.
func (r *bgJobRegistry) execute(ctx context.Context, job *bgJob, run bgJobRun) {
	// Bound concurrency: acquire a slot before starting the work so a burst of
	// fire-and-forget calls can't starve the rest of the system. Acquisition is
	// cancelable so a cancelled job never waits for a slot it no longer needs.
	if r.sem != nil {
		select {
		case r.sem <- struct{}{}:
			defer func() { <-r.sem }()
		case <-ctx.Done():
			job.mu.Lock()
			if job.Status == bgJobQueued {
				job.Status = bgJobCancelled
				now := time.Now().UTC()
				job.CompletedAt = &now
				job.Error = "cancelled before start"
			}
			job.mu.Unlock()
			r.persist(job)
			job.closeDone()
			return
		}
	}

	started := time.Now().UTC()
	job.mu.Lock()
	// A cancel can land between spawn() and this goroutine getting scheduled:
	// it would have already flipped Status to a terminal value (and closed
	// Done). Do NOT resurrect it to "running" or launch the work - bail out so
	// the job stays terminal and Done stays closed exactly once.
	if job.Status != bgJobQueued {
		job.mu.Unlock()
		job.closeDone()
		return
	}
	job.Status = bgJobRunning
	job.StartedAt = &started
	job.mu.Unlock()
	r.persist(job)

	output, err := run(ctx)

	completed := time.Now().UTC()
	job.mu.Lock()
	job.Output = output
	job.CompletedAt = &completed
	if ctx.Err() != nil {
		job.Status = bgJobCancelled
		job.Error = "cancelled by host"
	} else if err != nil {
		job.Status = bgJobFailed
		if job.Error == "" {
			job.Error = err.Error()
		}
	} else {
		job.Status = bgJobCompleted
	}
	job.mu.Unlock()
	// Persist the terminal snapshot BEFORE closing Done. Waiters (wait tool,
	// recovery, tests) select on Done as "the job is finished"; if we closed
	// it first, a waiter could resume and tear down the state dir while this
	// final file-write is still in flight.
	r.persist(job)
	job.closeDone()
	// Keep completed jobs queryable for a short window, then drop them from
	// the in-memory map. The on-disk JSON survives the full retention.
	go r.gc(job.ID, 10*time.Minute)
}

// snapshot returns a JSON-safe view of the job's current state. The in-memory
// handle is preferred (freshest data); on miss we fall back to the on-disk
// record so a poll after GC / after a core restart still gets an answer.
func (r *bgJobRegistry) snapshot(id string) (bgJobSnapshot, bool) {
	if r == nil {
		return bgJobSnapshot{}, false
	}
	r.mu.Lock()
	job, ok := r.jobs[id]
	r.mu.Unlock()
	if ok {
		return job.snapshotOf(), true
	}
	if rec, ok := r.readDisk(id); ok {
		return rec, true
	}
	return bgJobSnapshot{}, false
}

// wait blocks up to timeout for the job to reach a terminal state. Returns
// (true, snapshot) when the job is done; (false, snapshot) on timeout with
// the current status (typically still "running").
func (r *bgJobRegistry) wait(id string, timeout time.Duration) (bool, bgJobSnapshot) {
	if r == nil {
		return false, bgJobSnapshot{}
	}
	r.mu.Lock()
	job, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		// On-disk terminal record: short-circuit.
		if rec, ok := r.readDisk(id); ok && rec.Status.terminal() {
			return true, rec
		}
		return false, bgJobSnapshot{}
	}
	select {
	case <-job.Done:
		return true, job.snapshotOf()
	case <-time.After(timeout):
		return false, job.snapshotOf()
	}
}

// cancel asks the running job to stop and marks it cancelled. Returns the
// final snapshot so the caller can include it in the tool result text.
func (r *bgJobRegistry) cancel(id string) (bgJobSnapshot, bool) {
	if r == nil {
		return bgJobSnapshot{}, false
	}
	r.mu.Lock()
	job, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		// A disk-only terminal record can't be cancelled; report not found so
		// the caller surfaces a clear error instead of a no-op success.
		if rec, ok := r.readDisk(id); ok {
			return rec, true
		}
		return bgJobSnapshot{}, false
	}
	job.mu.Lock()
	if job.Status == bgJobRunning || job.Status == bgJobQueued {
		// Cancelling the context makes run() return; execute() then writes the
		// terminal state, persists it, and closes Done. We let execute() be the
		// one that closes Done so the "Done implies result-on-disk" invariant
		// holds and a waiter never races execute()'s final persist().
		cancelFn := job.Cancel
		done := job.Done
		job.mu.Unlock()
		if cancelFn != nil {
			cancelFn()
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			// Defensive fallback: execute() did not converge. Flip the status
			// ourselves so snapshot/wait stop returning "running" forever,
			// persist, and force the funnelled close.
			job.mu.Lock()
			if !job.Status.terminal() {
				job.Status = bgJobCancelled
				now := time.Now().UTC()
				job.CompletedAt = &now
				job.Error = "cancelled by host"
			}
			job.mu.Unlock()
			r.persist(job)
			job.closeDone()
		}
	} else {
		job.mu.Unlock()
	}
	return job.snapshotOf(), true
}

// terminal reports whether a status value is final.
func (s bgJobStatus) terminal() bool {
	switch s {
	case bgJobCompleted, bgJobFailed, bgJobCancelled:
		return true
	}
	return false
}

// persist writes the job record atomically to state/tool-jobs/<id>.json.
func (r *bgJobRegistry) persist(job *bgJob) {
	if r == nil {
		return
	}
	dir := r.root()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	snap := job.snapshotOf()
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	_ = writeFileAtomic(filepath.Join(dir, snap.ID+".json"), append(raw, '\n'), 0o600)
}

// readDisk reads a job record directly from disk, without consulting the
// in-memory map. Used for out-of-process recovery and for late lookups of
// jobs that were GC'd after completion.
func (r *bgJobRegistry) readDisk(id string) (bgJobSnapshot, bool) {
	dir := r.root()
	if dir == "" {
		return bgJobSnapshot{}, false
	}
	if strings.TrimSpace(id) == "" || filepath.Base(id) != id || strings.Contains(id, "..") {
		return bgJobSnapshot{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return bgJobSnapshot{}, false
	}
	var snap bgJobSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return bgJobSnapshot{}, false
	}
	return snap, true
}

// recover scans the on-disk dir for jobs that were left running by a crashed
// core and marks them as failed with a clear "runtime restarted" reason.
func (r *bgJobRegistry) recover() {
	if r == nil {
		return
	}
	dir := r.root()
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		rec, ok := r.readDisk(id)
		if !ok {
			continue
		}
		if !rec.Status.terminal() {
			rec.Status = bgJobFailed
			rec.Error = "runtime restarted before background job completed"
			rec.CompletedAt = &now
			raw, _ := json.MarshalIndent(rec, "", "  ")
			if raw != nil {
				_ = writeFileAtomic(filepath.Join(dir, id+".json"), append(raw, '\n'), 0o600)
			}
		}
		// Terminal jobs are loaded into the in-memory map so a polling agent
		// can read them for the retention window without hitting disk.
		recCopy := rec
		job := &bgJob{
			ID: recCopy.ID, ToolName: recCopy.ToolName, RunID: recCopy.RunID, SessionID: recCopy.SessionID,
			Status: recCopy.Status, Output: recCopy.Output, Error: recCopy.Error,
			CreatedAt: recCopy.CreatedAt, StartedAt: recCopy.StartedAt, CompletedAt: recCopy.CompletedAt,
			Done: make(chan struct{}),
		}
		close(job.Done)
		r.jobs[id] = job
	}
	r.evictIfNeededLocked()
}

// gc drops a completed job from the in-memory map after the retention window.
func (r *bgJobRegistry) gc(id string, after time.Duration) {
	time.Sleep(after)
	r.mu.Lock()
	delete(r.jobs, id)
	r.mu.Unlock()
}

// evictIfNeededLocked trims the in-memory map to the registry cap, dropping
// the least-recently-completed jobs first. Called with r.mu held.
func (r *bgJobRegistry) evictIfNeededLocked() {
	if r == nil || r.cap <= 0 || len(r.jobs) <= r.cap {
		return
	}
	type aged struct {
		id   string
		date time.Time
	}
	all := make([]aged, 0, len(r.jobs))
	for id, job := range r.jobs {
		job.mu.Lock()
		done := job.CompletedAt
		job.mu.Unlock()
		if done != nil {
			all = append(all, aged{id: id, date: *done})
		}
	}
	if len(all) == 0 {
		return
	}
	for len(r.jobs) > r.cap && len(all) > 0 {
		oldest := all[0]
		idx := 0
		for i, a := range all {
			if a.date.Before(oldest.date) {
				oldest = a
				idx = i
			}
		}
		delete(r.jobs, oldest.id)
		all = append(all[:idx], all[idx+1:]...)
	}
}

// bgJobsRoot returns the directory where background jobs are persisted. It
// prefers the orchestrator's configured state dir; falls back to the user
// data root so the tool still works in unit tests without a full runtime.
func (o *Orchestrator) bgJobsRoot() string {
	if o != nil && strings.TrimSpace(o.stateDir) != "" {
		return filepath.Join(o.stateDir, "tool-jobs")
	}
	return filepath.Join(configDataRootFallback(), "state", "tool-jobs")
}

// bgJobs returns the orchestrator's registry, creating it on first use. The
// registry is stored in a sync.Once-protected field so concurrent first calls
// do not race on the map init.
func (o *Orchestrator) bgJobs() *bgJobRegistry {
	if o == nil {
		return nil
	}
	o.bgJobsOnce.Do(func() {
		parallel := o.cfg.Orchestrator.WithDefaults().Continuation.MaxParallelTools
		o.bgJobsReg = newBgJobRegistry(o.bgJobsRoot(), parallel)
	})
	return o.bgJobsReg
}

// spawnBgTool enqueues a non-blocking tool run and returns the JSON the model
// sees immediately: {job_id, status:"queued", tool, hint}. Used by the
// non-blocking dispatch branch in runSharedTool / sub-agent dispatch. The
// actor run id is read off ctx (set via withActorRunID) so exec can persist
// the actor's CWD from the background goroutine; sessionID is best-effort
// metadata (empty for shared-tool dispatch, which has no session on ctx).
func (o *Orchestrator) spawnBgTool(ctx context.Context, toolName string, run bgJobRun) string {
	reg := o.bgJobs()
	if reg == nil {
		return "Error: background job registry unavailable."
	}
	job := reg.spawn(ctx, toolName, actorRunID(ctx), "", run)
	if job == nil {
		return "Error: background job registry unavailable."
	}
	snap := job.snapshotOf()
	out := map[string]any{
		"job_id":     snap.ID,
		"status":     string(snap.Status),
		"tool":       toolName,
		"queued":     true,
		"created_at": snap.CreatedAt.Format(time.RFC3339Nano),
		"hint":       "running in the background. Call wait {mode:'tool', job_id:'" + snap.ID + "'} to collect the result, or sapaloq_cancel_job {job_id:'" + snap.ID + "'} to abort.",
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf("Error: marshal job: %v", err)
	}
	return string(raw)
}

// bgJobToView projects a bgJobSnapshot to the JSON shape the model sees. The
// Output field is included only when the job is terminal, so polling does not
// transfer potentially-large buffers mid-run.
func bgJobToView(snap bgJobSnapshot) map[string]any {
	view := map[string]any{
		"job_id": snap.ID,
		"status": string(snap.Status),
		"tool":   snap.ToolName,
	}
	if !snap.CreatedAt.IsZero() {
		view["created_at"] = snap.CreatedAt.Format(time.RFC3339Nano)
	}
	if snap.StartedAt != nil {
		view["started_at"] = snap.StartedAt.Format(time.RFC3339Nano)
	}
	if snap.CompletedAt != nil {
		view["completed_at"] = snap.CompletedAt.Format(time.RFC3339Nano)
	}
	if snap.Status.terminal() {
		view["output"] = snap.Output
		if snap.Error != "" {
			view["error"] = snap.Error
		}
	}
	return view
}

// execBgRun builds the bgJobRun for a non-blocking `exec`. It wraps
// runShellCaptured (which enforces its own timeout and kills the whole
// process group on ctx cancel) and persists the actor's CWD when the command
// changes it - mirroring the blocking toolExec path so a fire-and-forget exec
// behaves exactly like an inline one, just collected later. runID is captured
// at dispatch time (from the ctx) so the background goroutine can persist the
// actor's CWD even though it runs on a fresh context.
func (o *Orchestrator) execBgRun(args toolArgs, runID string) bgJobRun {
	cmd := strings.TrimSpace(args.Command)
	cwd := args.Cwd
	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > maxTerminalSecs {
		timeout = maxTerminalSecs
	}
	return func(ctx context.Context) (string, error) {
		if cmd == "" {
			return "", fmt.Errorf("command is required")
		}
		res := runShellCaptured(ctx, cmd, cwd, time.Duration(timeout)*time.Second)
		if res.FinalCWD != "" && runID != "" {
			o.persistActorCWD(runID, res.FinalCWD)
		}
		text := res.Output
		switch {
		case res.TimedOut:
			return fmt.Sprintf("Command timed out after %ds (process group killed).\n%s", timeout, text), fmt.Errorf("timed out after %ds", timeout)
		case res.Cancelled:
			return fmt.Sprintf("Command cancelled by host.\n%s", text), fmt.Errorf("cancelled by host")
		case res.Err != nil:
			return fmt.Sprintf("Command exited with error: %v\n%s", res.Err, text), res.Err
		}
		if strings.TrimSpace(text) == "" {
			return "(command produced no output)", nil
		}
		return text, nil
	}
}
