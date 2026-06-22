package orchestrator

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

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/parse"
)

type toolJobStatus string

const (
	toolJobQueued    toolJobStatus = "queued"
	toolJobRunning   toolJobStatus = "running"
	toolJobCompleted toolJobStatus = "completed"
	toolJobFailed    toolJobStatus = "failed"
	toolJobCancelled toolJobStatus = "cancelled"
)

// toolJob is the durable state of one tool invocation. The event bus is only a
// wake-up/visibility channel; this record remains the source of truth.
type toolJob struct {
	ID          string         `json:"id"`
	RunID       string         `json:"run_id"`
	SessionID   string         `json:"session_id"`
	Index       int            `json:"index"`
	ToolCall    parse.ToolCall `json:"tool_call"`
	ResourceKey string         `json:"resource_key,omitempty"`
	Status      toolJobStatus  `json:"status"`
	Result      string         `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

type scheduledTool struct {
	index   int
	call    parse.ToolCall
	execute func(context.Context) turnOutcome
}

type toolJobResult struct {
	job     toolJob
	outcome turnOutcome
}

type toolJobScheduler struct {
	root string
	bus  interface {
		Publish(string, bridge.StreamEvent)
	}
	slots chan struct{}

	mu      sync.Mutex
	lanes   map[string]*sync.Mutex
	cancels map[string]context.CancelFunc
	seq     uint64
	update  chan struct{}
}

func newToolJobScheduler(root string, maxParallel int, eventBus interface {
	Publish(string, bridge.StreamEvent)
}) *toolJobScheduler {
	if maxParallel < 1 {
		maxParallel = 8
	}
	s := &toolJobScheduler{
		root:    root,
		bus:     eventBus,
		slots:   make(chan struct{}, maxParallel),
		lanes:   make(map[string]*sync.Mutex),
		cancels: make(map[string]context.CancelFunc),
		update:  make(chan struct{}),
	}
	s.recoverOrphaned()
	return s
}

func (s *toolJobScheduler) submitBatch(ctx context.Context, runID, sessionID string, tools []scheduledTool) <-chan toolJobResult {
	results := make(chan toolJobResult, len(tools))
	if len(tools) == 0 {
		close(results)
		return results
	}
	var wg sync.WaitGroup
	wg.Add(len(tools))
	for _, item := range tools {
		item := item
		now := time.Now().UTC()
		id := fmt.Sprintf("job-%d-%d", now.UnixNano(), atomic.AddUint64(&s.seq, 1))
		job := toolJob{
			ID:          id,
			RunID:       runID,
			SessionID:   sessionID,
			Index:       item.index,
			ToolCall:    item.call,
			ResourceKey: toolResourceKey(runID, item.call),
			Status:      toolJobQueued,
			CreatedAt:   now,
		}
		jobCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.cancels[id] = cancel
		s.mu.Unlock()
		s.persist(job)
		s.publish(job)
		go func() {
			defer wg.Done()
			defer cancel()
			defer func() {
				s.mu.Lock()
				delete(s.cancels, id)
				s.mu.Unlock()
			}()
			result := s.run(jobCtx, job, item.execute)
			select {
			case results <- result:
			case <-ctx.Done():
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	return results
}

func (s *toolJobScheduler) run(ctx context.Context, job toolJob, execute func(context.Context) turnOutcome) toolJobResult {
	if toolConsumesWorkerSlot(job.ToolCall.Name) {
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		case <-ctx.Done():
			return s.finishCancelled(job, ctx.Err())
		}
	}

	unlock, ok := s.acquireLane(ctx, job.ResourceKey)
	if !ok {
		return s.finishCancelled(job, ctx.Err())
	}
	defer unlock()

	started := time.Now().UTC()
	job.Status = toolJobRunning
	job.StartedAt = &started
	s.persist(job)
	s.publish(job)

	outcome := execute(ctx)
	completed := time.Now().UTC()
	job.CompletedAt = &completed
	job.Result = outcome.text
	if ctx.Err() != nil {
		job.Status = toolJobCancelled
		job.Error = ctx.Err().Error()
	} else if strings.HasPrefix(strings.TrimSpace(outcome.text), "Error:") ||
		strings.HasPrefix(strings.TrimSpace(outcome.text), "Command exited with error:") {
		job.Status = toolJobFailed
		job.Error = outcome.text
	} else {
		job.Status = toolJobCompleted
	}
	s.persist(job)
	s.publish(job)
	return toolJobResult{job: job, outcome: outcome}
}

func (s *toolJobScheduler) finishCancelled(job toolJob, err error) toolJobResult {
	now := time.Now().UTC()
	job.CompletedAt = &now
	job.Status = toolJobCancelled
	if err != nil {
		job.Error = err.Error()
	}
	s.persist(job)
	s.publish(job)
	return toolJobResult{job: job, outcome: turnOutcome{text: "Tool cancelled.", handled: true}}
}

func (s *toolJobScheduler) acquireLane(ctx context.Context, key string) (func(), bool) {
	if key == "" {
		return func() {}, true
	}
	s.mu.Lock()
	lane := s.lanes[key]
	if lane == nil {
		lane = &sync.Mutex{}
		s.lanes[key] = lane
	}
	s.mu.Unlock()

	acquired := make(chan struct{})
	go func() {
		lane.Lock()
		close(acquired)
	}()
	select {
	case <-acquired:
		return lane.Unlock, true
	case <-ctx.Done():
		// The lock goroutine may acquire later; release it immediately then.
		go func() {
			<-acquired
			lane.Unlock()
		}()
		return func() {}, false
	}
}

func (s *toolJobScheduler) cancelRun(runID string) {
	s.mu.Lock()
	var cancels []context.CancelFunc
	for id, cancel := range s.cancels {
		if job, ok := s.read(id); ok && job.RunID == runID {
			cancels = append(cancels, cancel)
		}
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *toolJobScheduler) persist(job toolJob) {
	if strings.TrimSpace(s.root) == "" {
		return
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return
	}
	_ = writeFileAtomic(filepath.Join(s.root, job.ID+".json"), data, 0o600)
}

func (s *toolJobScheduler) read(id string) (toolJob, bool) {
	var job toolJob
	if strings.TrimSpace(s.root) == "" {
		return job, false
	}
	data, err := os.ReadFile(filepath.Join(s.root, id+".json"))
	if err != nil || json.Unmarshal(data, &job) != nil {
		return toolJob{}, false
	}
	return job, true
}

func (s *toolJobScheduler) publish(job toolJob) {
	s.signalUpdate()
	if s.bus == nil {
		return
	}
	ev := bridge.NewEvent(bridge.EventToolUpdate)
	ev.SessionID = job.SessionID
	ev.RunID = job.RunID
	ev.JobID = job.ID
	ev.Status = string(job.Status)
	ev.Summary = job.ToolCall.Name
	if job.Error != "" {
		ev.Error = job.Error
	}
	s.bus.Publish("sapaloq.v1.tool."+string(job.Status), ev)
}

func (s *toolJobScheduler) signalUpdate() {
	s.mu.Lock()
	close(s.update)
	s.update = make(chan struct{})
	s.mu.Unlock()
}

func (s *toolJobScheduler) recoverOrphaned() {
	if strings.TrimSpace(s.root) == "" {
		return
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, ok := s.read(strings.TrimSuffix(entry.Name(), ".json"))
		if !ok || job.Status != toolJobQueued && job.Status != toolJobRunning {
			continue
		}
		now := time.Now().UTC()
		job.Status = toolJobCancelled
		job.Error = "runtime restarted before tool job completed"
		job.CompletedAt = &now
		s.persist(job)
	}
}

func toolConsumesWorkerSlot(name string) bool {
	switch name {
	case "sapaloq_wait", "sapaloq_wait_events", "sapaloq_wait_jobs":
		return false
	default:
		return true
	}
}

func toolResourceKey(runID string, call parse.ToolCall) string {
	args := parseToolArgs(call.Arguments)
	switch call.Name {
	case "write_file", "create_file", "edit_file", "delete_file":
		return "path:" + filepath.Clean(expandHome(args.Path))
	case "write_plan":
		return "actor:" + runID + ":plan"
	case "sapaloq_complete_task", "sapaloq_fail_task", "request_clarification",
		"sapaloq_update_task_progress", "sapaloq_stop", "sapaloq_answer_clarification":
		return "actor:" + runID + ":lifecycle"
	case "exec":
		// Commands sharing a cwd are conservatively serialized because their
		// filesystem side effects are opaque. Explicit file tools remain
		// parallel across distinct paths.
		return "cwd:" + filepath.Clean(expandHome(args.Cwd))
	default:
		return ""
	}
}
