package orchestrator

// task_inspect.go exposes the per-task detail the widget's "Planner & Agent"
// pop-up needs: the durable task record (status.json), a tail of the progress
// stream (orch-<taskID>.jsonl), and the persisted plan markdown. It is the
// read-only surface the frontend polls every couple of seconds while a
// sub-agent is active, so a user can watch a planner/agent think, call tools,
// and write its plan without leaving the chat.
//
// The progress JSONL is read straight off disk (the same file the
// asyncProgressWriter appends to), so TaskInspect is safe to call from the IPC
// server without coordinating with the live sub-agent goroutine. The
// afterLine parameter lets the frontend fetch only the events appended since
// its last poll, keeping each response bounded even for long runs.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// maxTaskInspectEvents caps the number of progress events returned in one
// TaskInspect response. The frontend polls incrementally (afterLine), so this
// only bounds a single cold-open of a very long run; the tail is the most
// recent events, which is what a user opening the pop-up mid-run wants to see.
const maxTaskInspectEvents = 2000

// TaskInspectResult is the JSON shape the widget renders in the pop-up. The
// Events slice is the progress tail (newest last); EventCount is the total
// line count on disk so the frontend can request the next incremental slice.
type TaskInspectResult struct {
	ID         string               `json:"id"`
	Role       string               `json:"role"`
	Status     string               `json:"status"`
	Task       string               `json:"task"`
	Result     string               `json:"result,omitempty"`
	Error      string               `json:"error,omitempty"`
	Question   string               `json:"question,omitempty"`
	PlanTaskID string               `json:"plan_task_id,omitempty"`
	Plan       string               `json:"plan,omitempty"`
	Events     []bridge.StreamEvent `json:"events"`
	EventCount int                  `json:"event_count"`
	UpdatedAt  time.Time            `json:"updated_at"`
}

// TaskInspect returns the durable state + a tail of the progress stream for a
// sub-agent task, plus its plan markdown when present. afterLine is the number
// of progress lines the caller has already seen (0 on first open); only lines
// after that index are returned, up to maxTaskInspectEvents. An unknown or
// path-malformed task id returns an error so the frontend can show "tidak
// aktif" instead of a half-empty panel.
func (o *Orchestrator) TaskInspect(taskID string, afterLine int) (TaskInspectResult, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || filepath.Base(taskID) != taskID || strings.Contains(taskID, "..") {
		return TaskInspectResult{}, fmt.Errorf("valid task_id is required")
	}
	record, err := o.readTask(taskID)
	if err != nil {
		return TaskInspectResult{}, err
	}
	result := TaskInspectResult{
		ID:         record.ID,
		Role:       record.Role,
		Status:     record.Status,
		Task:       record.Task,
		Result:     record.Result,
		Error:      record.Error,
		Question:   record.Question,
		PlanTaskID: record.PlanTaskID,
		UpdatedAt:  record.UpdatedAt,
	}
	// Plan: a planner's own plan.md is its authoritative output; an agent
	// executing a handed-off plan exposes that plan via PlanTaskID so the
	// user can see what the agent is working from.
	planID := record.ID
	if record.Role != "planner" && record.PlanTaskID != "" {
		planID = record.PlanTaskID
	}
	result.Plan = o.readPlanMarkdown(planID)

	events, total, err := o.readProgressTail(taskID, afterLine)
	if err != nil {
		// A missing/empty progress file is not fatal - the task may be
		// pending or a planner that hasn't emitted yet. Surface the record
		// with an empty stream rather than failing the whole inspect.
		result.Events = []bridge.StreamEvent{}
		result.EventCount = total
		return result, nil
	}
	result.Events = events
	result.EventCount = total
	return result, nil
}

// readProgressTail parses orch-<taskID>.jsonl and returns the events after
// afterLine (newest last), plus the total line count on disk. It bounds the
// returned slice to maxTaskInspectEvents by keeping the most recent lines.
func (o *Orchestrator) readProgressTail(taskID string, afterLine int) ([]bridge.StreamEvent, int, error) {
	dir := o.progressDir()
	if dir == "" {
		return nil, 0, fmt.Errorf("progress dir unavailable")
	}
	path := filepath.Join(dir, "orch-"+taskID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	var (
		out   []bridge.StreamEvent
		total int
		tail  []bridge.StreamEvent // ring buffer for the cap
	)
	sc := bufio.NewScanner(f)
	// A single stream event can carry a large attachment/tool body; allow up
	// to the IPC frame cap per line so a fat tool_call doesn't abort the scan.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		total++
		if total <= afterLine {
			continue
		}
		var ev bridge.StreamEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			// Skip a malformed/truncated line (e.g. a write mid-flush) rather
			// than failing the whole inspect; the next poll re-reads it.
			continue
		}
		if len(tail) >= maxTaskInspectEvents {
			tail = tail[1:]
		}
		tail = append(tail, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, total, err
	}
	out = make([]bridge.StreamEvent, len(tail))
	copy(out, tail)
	return out, total, nil
}

// progressDir returns the on-disk directory where per-task progress JSONL
// files live. It mirrors the ProgressWriter.Dir the orchestrator was
// constructed with; safe to call even when the async writer is nil.
func (o *Orchestrator) progressDir() string {
	if o == nil {
		return ""
	}
	if o.progress != nil {
		return o.progress.inner.Dir
	}
	return ""
}
