package orchestrator

// tools_async_exec.go provides non-blocking shell execution so a sub-agent can
// stay alive when a single long-running command would otherwise wedge its turn
// loop. The history is the recurring "tool_call stuck" bug: the model issues
// `exec`, the dispatcher blocks waiting for the command to finish, and if the
// post-tool chat completion hangs at the provider, the agent never gets a tool
// result back — meanwhile its heartbeat ticker keeps the worker "healthy" so
// the watchdog never fires. The fix is to make exec a real subtask:
//
//   - exec_async: spawn a job and return {job_id, status:"queued"} immediately.
//   - exec_status: poll a job's current status without waiting.
//   - exec_result: block up to wait_seconds for the job to finish; if it is
//     still running, return {status:"running", waited_ms, hint:"call again or
//     exec_cancel"}. This is the timeout-guard that lets a sub-agent
//     self-fail (sapaloq_fail_task) when a command hangs longer than expected.
//   - exec_cancel: cancel a running job.
//
// Jobs persist to state/tool-jobs/<id>.json so a crashed core can recover /
// list them on the next boot. The on-disk schema is intentionally compatible
// with toolJobScheduler (same Status enum) so the existing recovery path can
// reason about both kinds of job with one code path.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// asyncExecStatus mirrors toolJobStatus. The values are persisted as strings
// (queued/running/completed/failed/cancelled) so the same JSON can be read
// from disk during recovery and by other orchestrator tooling.
type asyncExecStatus string

const (
	asyncExecQueued    asyncExecStatus = "queued"
	asyncExecRunning   asyncExecStatus = "running"
	asyncExecCompleted asyncExecStatus = "completed"
	asyncExecFailed    asyncExecStatus = "failed"
	asyncExecCancelled asyncExecStatus = "cancelled"
)

// asyncExecJob is the durable + live state of one async exec invocation.
// The on-disk representation is asyncExecSnapshot (a struct without the
// in-process fields like mu, Cancel, Done) so a JSON round-trip never
// serializes a mutex. Live callers should hold *asyncExecJob.
type asyncExecJob struct {
	ID          string          `json:"id"`
	RunID       string          `json:"run_id,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
	Command     string          `json:"command"`
	Cwd         string          `json:"cwd,omitempty"`
	TimeoutSec  int             `json:"timeout_seconds,omitempty"`
	Status      asyncExecStatus `json:"status"`
	Output      string          `json:"output,omitempty"`
	Error       string          `json:"error,omitempty"`
	ExitCode    int             `json:"exit_code,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	// Cancel is populated only in-process; it is dropped from JSON.
	Cancel context.CancelFunc `json:"-"`
	// Done is closed when Status is terminal. Pollers select on this channel
	// instead of busy-waiting on the status field. Both execute() (natural
	// finish) and cancel() can reach the terminal state, so the close is
	// funnelled through doneOnce to avoid a double-close panic when a cancel
	// races the command's own completion.
	Done chan struct{} `json:"-"`
	// doneOnce guards exactly one close(Done) across the execute()/cancel()
	// race. Never serialized.
	doneOnce sync.Once `json:"-"`
	// mu guards the live mutable fields (Status, Output, Error, ExitCode,
	// StartedAt, CompletedAt) so concurrent status / result / cancel calls
	// never see a torn write. The on-disk file is written after releasing mu.
	mu sync.Mutex `json:"-"`
}

// asyncExecSnapshot is the JSON-safe view of a job. It is what gets written to
// disk and what callers pass to asyncJobToView. Building one requires holding
// job.mu and then projecting fields by value.
type asyncExecSnapshot struct {
	ID          string          `json:"id"`
	RunID       string          `json:"run_id,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
	Command     string          `json:"command"`
	Cwd         string          `json:"cwd,omitempty"`
	TimeoutSec  int             `json:"timeout_seconds,omitempty"`
	Status      asyncExecStatus `json:"status"`
	Output      string          `json:"output,omitempty"`
	Error       string          `json:"error,omitempty"`
	ExitCode    int             `json:"exit_code,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

// snapshotOf builds the JSON-safe view while holding the live job's mutex.
func (j *asyncExecJob) snapshotOf() asyncExecSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	return asyncExecSnapshot{
		ID:          j.ID,
		RunID:       j.RunID,
		SessionID:   j.SessionID,
		Command:     j.Command,
		Cwd:         j.Cwd,
		TimeoutSec:  j.TimeoutSec,
		Status:      j.Status,
		Output:      j.Output,
		Error:       j.Error,
		ExitCode:    j.ExitCode,
		CreatedAt:   j.CreatedAt,
		StartedAt:   j.StartedAt,
		CompletedAt: j.CompletedAt,
	}
}

// closeDone closes the Done channel exactly once, even when execute() and
// cancel() both reach a terminal state for the same job (a cancel racing the
// command's natural completion). Without this funnel the second close panics
// with "close of closed channel".
func (j *asyncExecJob) closeDone() {
	j.doneOnce.Do(func() { close(j.Done) })
}

// terminal reports whether the job has reached a final state. The terminal
// states are completed / failed / cancelled.
func (j *asyncExecJob) terminal() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch j.Status {
	case asyncExecCompleted, asyncExecFailed, asyncExecCancelled:
		return true
	}
	return false
}

// asyncExecRegistry is the in-process roster of live jobs. It is the
// authoritative state for in-flight jobs; the JSON files in state/tool-jobs/
// are just snapshots written on every transition.
type asyncExecRegistry struct {
	dir string
	mu  sync.Mutex
	seq uint64
	// jobs holds only non-terminal jobs plus recently-completed ones within
	// the retention window. Once a job is GC'd it is still on disk; the
	// in-memory map is just a fast path for active polling.
	jobs map[string]*asyncExecJob
	// cap is the soft cap on in-memory jobs before we evict.
	cap int
}

func newAsyncExecRegistry(dir string) *asyncExecRegistry {
	r := &asyncExecRegistry{
		dir:  dir,
		jobs: make(map[string]*asyncExecJob),
		cap:  256,
	}
	r.recover()
	return r
}

// root returns the on-disk directory for async-exec job snapshots, even when
// the registry is nil. Used by tests and recovery helpers.
func (r *asyncExecRegistry) root() string {
	if r == nil || strings.TrimSpace(r.dir) == "" {
		return ""
	}
	return r.dir
}

// spawn enqueues a new job and starts its background goroutine. Returns the
// live handle (for in-process polling). The goroutine writes its terminal
// state to disk and into the registry under a single mu lock.
func (r *asyncExecRegistry) spawn(ctx context.Context, runID, sessionID, cmd, cwd string, timeoutSec int) *asyncExecJob {
	if r == nil {
		return nil
	}
	now := time.Now().UTC()
	job := &asyncExecJob{
		ID:         fmt.Sprintf("ajob-%d-%d", now.UnixNano(), atomic.AddUint64(&r.seq, 1)),
		RunID:      runID,
		SessionID:  sessionID,
		Command:    cmd,
		Cwd:        cwd,
		TimeoutSec: timeoutSec,
		Status:     asyncExecQueued,
		CreatedAt:  now,
		Done:       make(chan struct{}),
	}
	runCtx, cancel := context.WithCancel(context.Background())
	job.Cancel = cancel
	r.mu.Lock()
	r.jobs[job.ID] = job
	r.evictIfNeededLocked()
	r.mu.Unlock()
	r.persist(job)
	go r.execute(runCtx, job, cmd, cwd, timeoutSec)
	return job
}

// execute runs the command and transitions the job to a terminal state. It
// is the only goroutine that mutates the job's status/output once it has left
// "queued". Cancel from elsewhere calls job.Cancel which fires runCtx and
// triggers the "Command exited with error: signal: killed" path below.
func (r *asyncExecRegistry) execute(ctx context.Context, job *asyncExecJob, cmd, cwd string, timeoutSec int) {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	if timeoutSec > maxTerminalSecs {
		timeoutSec = maxTerminalSecs
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	started := time.Now().UTC()
	job.mu.Lock()
	// A cancel can land between spawn() and this goroutine getting scheduled:
	// it would have already flipped Status to a terminal value (and closed
	// Done). Do NOT resurrect it to "running" or launch the command — bail out
	// so the job stays terminal and Done stays closed exactly once.
	if job.Status != asyncExecQueued {
		job.mu.Unlock()
		job.closeDone() // no-op if already closed by the canceller
		return
	}
	job.Status = asyncExecRunning
	job.StartedAt = &started
	job.mu.Unlock()
	r.persist(job)

	const cwdMarker = "__SAPALOQ_FINAL_CWD__="
	wrapped := cmd + "\nstatus=$?\nprintf '\\n" + cwdMarker + "%s\\n' \"$PWD\"\nexit $status"
	c := exec.CommandContext(runCtx, "bash", "-lc", wrapped)
	if dir := strings.TrimSpace(cwd); dir != "" {
		c.Dir = expandHome(dir)
	}
	out, err := c.CombinedOutput()
	text, finalCWD := splitExecCWD(string(out), cwdMarker)
	if finalCWD != "" && job.RunID != "" {
		(&Orchestrator{}).persistActorCWD(job.RunID, finalCWD)
	}
	if len(text) > 16*1024 {
		text = text[:16*1024] + "\n[output truncated]"
	}
	completed := time.Now().UTC()
	job.mu.Lock()
	job.Output = text
	job.CompletedAt = &completed
	if ctx.Err() != nil {
		job.Status = asyncExecCancelled
		job.Error = "cancelled by host"
	} else if runCtx.Err() == context.DeadlineExceeded {
		job.Status = asyncExecFailed
		job.Error = fmt.Sprintf("Command timed out after %ds.", timeoutSec)
	} else if err != nil {
		job.Status = asyncExecFailed
		job.ExitCode = -1
		job.Error = fmt.Sprintf("Command exited with error: %v", err)
	} else {
		job.Status = asyncExecCompleted
		job.ExitCode = 0
	}
	job.mu.Unlock()
	// Persist the terminal snapshot BEFORE closing Done. Waiters (exec_result,
	// recovery, tests) select on Done as "the job is finished"; if we closed it
	// first, a waiter could resume and tear down the state dir while this final
	// file-write is still in flight (the flaky "TempDir RemoveAll: directory
	// not empty"). Closing after persist makes Done imply "result on disk".
	r.persist(job)
	job.closeDone()
	// Keep completed jobs queryable for a short window, then drop them from
	// the in-memory map. The on-disk JSON survives the full retention so an
	// out-of-process recovery still finds the result.
	go r.gc(job.ID, 10*time.Minute)
}

// snapshot returns a JSON-safe view of the job's current state. The in-memory
// handle is preferred (freshest data); on miss we fall back to the on-disk
// record so a poll after GC / after a core restart still gets an answer.
func (r *asyncExecRegistry) snapshot(id string) (asyncExecSnapshot, bool) {
	if r == nil {
		return asyncExecSnapshot{}, false
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
	return asyncExecSnapshot{}, false
}

// wait blocks up to timeout for the job to reach a terminal state. Returns
// (true, snapshot) when the job is done; (false, snapshot) on timeout with
// the current status (typically still "running").
func (r *asyncExecRegistry) wait(id string, timeout time.Duration) (bool, asyncExecSnapshot) {
	if r == nil {
		return false, asyncExecSnapshot{}
	}
	r.mu.Lock()
	job, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		// On-disk terminal record: short-circuit.
		if rec, ok := r.readDisk(id); ok && rec.Status.terminal() {
			return true, rec
		}
		return false, asyncExecSnapshot{}
	}
	select {
	case <-job.Done:
		return true, job.snapshotOf()
	case <-time.After(timeout):
		return false, job.snapshotOf()
	}
}

// cancel asks the host OS to kill the running command and marks the job
// cancelled. Returns the final snapshot so the caller can include it in the
// tool result text.
func (r *asyncExecRegistry) cancel(id string) (asyncExecSnapshot, bool) {
	if r == nil {
		return asyncExecSnapshot{}, false
	}
	r.mu.Lock()
	job, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		return asyncExecSnapshot{}, false
	}
	job.mu.Lock()
	if job.Status == asyncExecRunning || job.Status == asyncExecQueued {
		// execute() will see ctx.Err() != nil and write the terminal state.
		job.Cancel()
		// Defensive: if the goroutine somehow missed it, flip the status so
		// exec_status / exec_result stop returning "running" forever.
		job.Status = asyncExecCancelled
		now := time.Now().UTC()
		job.CompletedAt = &now
		job.Error = "cancelled by host"
		job.mu.Unlock()
		r.persist(job)
		// Funnelled close: execute() may also be transitioning this job to a
		// terminal state right now (the cancel raced the command finishing),
		// and it closes Done too. closeDone() makes whichever loses the race a
		// no-op instead of a double-close panic.
		job.closeDone()
	} else {
		job.mu.Unlock()
	}
	return job.snapshotOf(), true
}

// terminal reports whether a status value is final. The terminal states are
// completed / failed / cancelled.
func (s asyncExecStatus) terminal() bool {
	switch s {
	case asyncExecCompleted, asyncExecFailed, asyncExecCancelled:
		return true
	}
	return false
}

// persist writes the job record atomically to state/tool-jobs/<id>.json.
// It is called on every state transition so a crashed core can recover
// in-flight and recently-finished jobs from disk on the next boot.
func (r *asyncExecRegistry) persist(job *asyncExecJob) {
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
func (r *asyncExecRegistry) readDisk(id string) (asyncExecSnapshot, bool) {
	dir := r.root()
	if dir == "" {
		return asyncExecSnapshot{}, false
	}
	if strings.TrimSpace(id) == "" || filepath.Base(id) != id || strings.Contains(id, "..") {
		return asyncExecSnapshot{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return asyncExecSnapshot{}, false
	}
	var snap asyncExecSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return asyncExecSnapshot{}, false
	}
	return snap, true
}

// recover scans the on-disk dir for jobs that were left running by a crashed
// core and marks them as failed with a clear "runtime restarted" reason. The
// snapshot also re-loads terminal jobs into the in-memory map for the
// retention window so a fresh core can answer exec_status for a moment.
func (r *asyncExecRegistry) recover() {
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
			rec.Status = asyncExecFailed
			rec.Error = "runtime restarted before async exec completed"
			rec.CompletedAt = &now
			raw, _ := json.MarshalIndent(rec, "", "  ")
			if raw != nil {
				_ = writeFileAtomic(filepath.Join(dir, id+".json"), append(raw, '\n'), 0o600)
			}
		}
		// Terminal jobs are loaded into the in-memory map so a polling agent
		// can read them for the retention window without hitting the disk on
		// every call. GC will drop them after cap or after gc() fires.
		recCopy := rec
		job := &asyncExecJob{
			ID: recCopy.ID, RunID: recCopy.RunID, SessionID: recCopy.SessionID,
			Command: recCopy.Command, Cwd: recCopy.Cwd, TimeoutSec: recCopy.TimeoutSec,
			Status: recCopy.Status, Output: recCopy.Output, Error: recCopy.Error,
			ExitCode: recCopy.ExitCode, CreatedAt: recCopy.CreatedAt,
			StartedAt: recCopy.StartedAt, CompletedAt: recCopy.CompletedAt,
			Done: make(chan struct{}),
		}
		close(job.Done)
		r.jobs[id] = job
	}
	r.evictIfNeededLocked()
}

// gc drops a completed job from the in-memory map after the retention window.
// The on-disk record stays around (we only drop it on disk when the file
// actually causes the directory to grow, which is a future cleanup).
func (r *asyncExecRegistry) gc(id string, after time.Duration) {
	time.Sleep(after)
	r.mu.Lock()
	delete(r.jobs, id)
	r.mu.Unlock()
}

// evictIfNeededLocked trims the in-memory map to the registry cap. It is
// called with r.mu held. The eviction order is oldest-CompletedAt first so
// we drop the least-recently-completed jobs first.
func (r *asyncExecRegistry) evictIfNeededLocked() {
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
	// Drop the oldest completed jobs until we are at the cap.
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

// asyncExecRoot returns the directory where async exec jobs are persisted. It
// prefers the orchestrator's configured state dir; falls back to the user
// data root so the tool still works in unit tests without a full runtime.
func (o *Orchestrator) asyncExecRoot() string {
	if o != nil && strings.TrimSpace(o.stateDir) != "" {
		return filepath.Join(o.stateDir, "tool-jobs")
	}
	return filepath.Join(configDataRootFallback(), "state", "tool-jobs")
}

// asyncExecs returns the orchestrator's registry, creating it on first use.
// The registry is stored in a sync.Once-protected field so concurrent first
// calls do not race on the map init.
func (o *Orchestrator) asyncExecs() *asyncExecRegistry {
	if o == nil {
		return nil
	}
	o.asyncOnce.Do(func() {
		o.asyncExecReg = newAsyncExecRegistry(o.asyncExecRoot())
	})
	return o.asyncExecReg
}

// toolExecAsync spawns a non-blocking exec and returns the job_id. The model
// is expected to follow up with exec_status / exec_result / exec_cancel.
func (o *Orchestrator) toolExecAsync(ctx context.Context, args toolArgs) string {
	args = o.resolveActorArgs(ctx, args)
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		return "Error: command is required."
	}
	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > maxTerminalSecs {
		timeout = maxTerminalSecs
	}
	reg := o.asyncExecs()
	if reg == nil {
		return "Error: async exec registry unavailable."
	}
	job := reg.spawn(ctx, actorRunID(ctx), "", cmd, args.Cwd, timeout)
	if job == nil {
		return "Error: async exec registry unavailable."
	}
	out := map[string]any{
		"job_id":          job.ID,
		"status":          string(job.Status),
		"command":         cmd,
		"cwd":             args.Cwd,
		"timeout_seconds": timeout,
		"created_at":      job.CreatedAt.Format(time.RFC3339Nano),
		"hint":            "use exec_status(job_id) to poll and exec_result(job_id, wait_seconds) to fetch the output; call exec_cancel(job_id) to abort.",
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf("Error: marshal job: %v", err)
	}
	return string(raw)
}

// toolExecStatus returns a JSON snapshot of a job's current state. Cheap
// (in-memory read under a per-job mutex). Intended for tight polling loops.
func (o *Orchestrator) toolExecStatus(_ context.Context, args toolArgs) string {
	id := strings.TrimSpace(args.JobID)
	if id == "" {
		return "Error: job_id is required."
	}
	reg := o.asyncExecs()
	if reg == nil {
		return "Error: async exec registry unavailable."
	}
	snap, ok := reg.snapshot(id)
	if !ok {
		return fmt.Sprintf("Error: job_id %q not found.", id)
	}
	view := asyncJobToView(snap)
	raw, err := json.Marshal(view)
	if err != nil {
		return fmt.Sprintf("Error: marshal status: %v", err)
	}
	return string(raw)
}

// toolExecResult blocks up to args.WaitSeconds (default 30, max 300) for the
// job to reach a terminal state. When the job is still running after the
// wait window, the response is a small JSON {status:"running", waited_ms,
// hint} so the model can decide whether to keep polling, give up, or call
// sapaloq_fail_task. This is the timeout-guard the prompt teaches the agent
// to use as a self-fail trigger.
func (o *Orchestrator) toolExecResult(_ context.Context, args toolArgs) string {
	id := strings.TrimSpace(args.JobID)
	if id == "" {
		return "Error: job_id is required."
	}
	wait := time.Duration(args.WaitSeconds) * time.Second
	if args.WaitSeconds <= 0 {
		wait = 30 * time.Second
	}
	if wait > 300*time.Second {
		wait = 300 * time.Second
	}
	reg := o.asyncExecs()
	if reg == nil {
		return "Error: async exec registry unavailable."
	}
	start := time.Now()
	done, snap := reg.wait(id, wait)
	elapsed := time.Since(start)
	view := asyncJobToView(snap)
	view["waited_ms"] = elapsed.Milliseconds()
	if !done {
		// The job is still in flight. Tell the model explicitly so it does
		// not loop forever — the canonical escape hatch is sapaloq_fail_task.
		view["hint"] = "job is still running. Either call exec_result again with a wait_seconds, exec_cancel(job_id) to abort, or sapaloq_fail_task with reason='tool hang' if it has been too long."
	} else {
		delete(view, "hint")
	}
	raw, err := json.Marshal(view)
	if err != nil {
		return fmt.Sprintf("Error: marshal result: %v", err)
	}
	return string(raw)
}

// toolExecCancel kills a running job and returns the snapshot. The snapshot
// includes the partial output captured up to the cancel signal so the agent
// still has something to report.
func (o *Orchestrator) toolExecCancel(_ context.Context, args toolArgs) string {
	id := strings.TrimSpace(args.JobID)
	if id == "" {
		return "Error: job_id is required."
	}
	reg := o.asyncExecs()
	if reg == nil {
		return "Error: async exec registry unavailable."
	}
	snap, ok := reg.cancel(id)
	if !ok {
		return fmt.Sprintf("Error: job_id %q not found.", id)
	}
	view := asyncJobToView(snap)
	raw, err := json.Marshal(view)
	if err != nil {
		return fmt.Sprintf("Error: marshal cancel: %v", err)
	}
	return string(raw)
}

// asyncJobToView projects an asyncExecSnapshot to the JSON shape the model
// sees. The Output field is included only when the job is terminal, so
// polling does not transfer potentially-large buffers mid-run.
func asyncJobToView(snap asyncExecSnapshot) map[string]any {
	view := map[string]any{
		"job_id":  snap.ID,
		"status":  string(snap.Status),
		"command": snap.Command,
		"cwd":     snap.Cwd,
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
		view["exit_code"] = snap.ExitCode
		view["output"] = snap.Output
		if snap.Error != "" {
			view["error"] = snap.Error
		}
	}
	return view
}
