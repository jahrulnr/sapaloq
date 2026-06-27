package orchestrator

// task_inspect.go exposes per-actor detail for the widget sub-agent monitor:
// durable task status (status.json), coalesced transcript from turns.json, and
// plan markdown. ActorInspect hydrates cold-open; live updates arrive on the
// transcript bus (actor_id) while the monitor is open.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

type ActorInspectResult = TaskInspectResult

// ActorInspect is the actor-generic transcript read API. Background actors use
// task-scoped turns/checkpoints and rollout data; the legacy TaskInspect API is
// retained for one compatibility release.
func (o *Orchestrator) ActorInspect(actorID string, afterLine int) (ActorInspectResult, error) {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" || filepath.Base(actorID) != actorID || strings.Contains(actorID, "..") {
		return ActorInspectResult{}, fmt.Errorf("valid actor_id is required")
	}
	record, err := o.readTask(actorID)
	if err != nil {
		return ActorInspectResult{}, err
	}
	result := ActorInspectResult{
		ID: record.ID, Role: record.Role, Status: record.Status, Task: record.Task,
		Result: record.Result, Error: record.Error, Question: record.Question,
		PlanTaskID: record.PlanTaskID, UpdatedAt: record.UpdatedAt,
	}
	planID := record.ID
	if record.Role != "planner" && record.PlanTaskID != "" {
		planID = record.PlanTaskID
	}
	result.Plan = o.readPlanMarkdown(planID)
	entries, transcriptErr := o.SessionTranscript(context.Background(), actorID)
	if transcriptErr != nil {
		entries = []bridge.TranscriptEntry{}
	}
	result.EventCount = len(entries)
	if afterLine > 0 && afterLine < len(entries) {
		entries = entries[afterLine:]
	} else if afterLine >= len(entries) {
		entries = []bridge.TranscriptEntry{}
	}
	if len(entries) > maxTaskInspectEvents {
		entries = entries[len(entries)-maxTaskInspectEvents:]
	}
	result.Transcript = entries
	if usage, uerr := o.ContextUsage(context.Background(), actorID); uerr == nil {
		result.Usage = &bridge.TranscriptUsage{
			UsedTokens: usage.UsedTokens, ContextWindow: usage.ContextWindow,
			Percent: usage.Percent, Provider: usage.Provider, Model: usage.Model,
		}
	}
	return result, nil
}

// maxTaskInspectEvents bounds a single cold-open hydrate response.
const maxTaskInspectEvents = 2000

// TaskInspectResult is the JSON shape the widget renders in the pop-up. The
// Events slice is the progress tail (newest last); EventCount is the total
// line count on disk so the frontend can request the next incremental slice.
type TaskInspectResult struct {
	ID         string                   `json:"id"`
	Role       string                   `json:"role"`
	Status     string                   `json:"status"`
	Task       string                   `json:"task"`
	Result     string                   `json:"result,omitempty"`
	Error      string                   `json:"error,omitempty"`
	Question   string                   `json:"question,omitempty"`
	PlanTaskID string                   `json:"plan_task_id,omitempty"`
	Plan       string                   `json:"plan,omitempty"`
	Transcript []bridge.TranscriptEntry `json:"transcript,omitempty"`
	Usage      *bridge.TranscriptUsage   `json:"usage,omitempty"`
	EventCount int                      `json:"event_count"`
	UpdatedAt  time.Time                `json:"updated_at"`
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
		result.Transcript = []bridge.TranscriptEntry{}
		result.EventCount = total
		return result, nil
	}
	result.Transcript = CoalesceEvents(taskID, events)
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
