package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/ipc"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

type ipcRequest = ipc.Request

type ipcResponse = ipc.Response

// maxFrameBytes caps a single newline-delimited IPC frame, matching the core
// server limit. It must exceed the 8 MB attachment cap after base64 inflation.
const maxFrameBytes = 16 * 1024 * 1024

type pingResult struct {
	OK          bool   `json:"ok"`
	Message     string `json:"message"`
	RingState   string `json:"ring_state"`
	ServerMs    int64  `json:"server_ms"`
	RoundTripMs int64  `json:"round_trip_ms"`
	SocketPath  string `json:"socket_path"`
}

func defaultSocketPath() string {
	if p := os.Getenv("SAPALOQ_SOCKET"); p != "" {
		return p
	}
	return filepath.Join(os.Getenv("HOME"), "SapaLOQ", "run", "sapaloq.sock")
}

type chatResult struct {
	OK           bool                     `json:"ok"`
	SessionID    string                   `json:"session_id,omitempty"`
	GenerationID string                   `json:"generation_id,omitempty"`
	Transcript   []bridge.TranscriptEntry   `json:"transcript,omitempty"`
	Usage        *chatUsage               `json:"usage,omitempty"`
}

type chatTurn struct {
	ID              int64  `json:"id"`
	Seq             int    `json:"seq"`
	Role            string `json:"role"`
	Content         string `json:"content"`
	CheckpointIndex int    `json:"checkpoint_index,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	// Archived is derived from included_in_context=0 + the presence of a later
	// checkpoint: the turn is still shown (full transcript) but rendered muted
	// as pre-checkpoint history. The widget computes it from the checkpoint
	// boundary rather than stored, so we keep it lightweight here.
	Archived bool `json:"archived,omitempty"`
}

type chatUsage struct {
	SessionID      string `json:"session_id"`
	UsedTokens     int    `json:"used_tokens"`
	ContextWindow  int    `json:"context_window"`
	Percent        int    `json:"percent"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	CompactedTurns int    `json:"compacted_turns"`
	ActiveTurns    int    `json:"active_turns"`
}

type chatHistoryResult struct {
	OK         bool                     `json:"ok"`
	SessionID  string                   `json:"session_id"`
	Transcript []bridge.TranscriptEntry `json:"transcript,omitempty"`
	Usage      *chatUsage               `json:"usage,omitempty"`
}

type sessionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Active    bool   `json:"active"`
	TurnCount int    `json:"turn_count"`
	UpdatedAt string `json:"updated_at"`
	CreatedAt string `json:"created_at"`
}

type sessionListResult struct {
	OK       bool             `json:"ok"`
	Sessions []sessionSummary `json:"sessions"`
}

type actorRuntimeStatus struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	Phase     string `json:"phase"`
	Workspace string `json:"workspace"`
}

type runtimeStatus struct {
	Provider      string               `json:"provider"`
	Model         string               `json:"model"`
	Driver        string               `json:"driver"`
	Reasoning     string               `json:"reasoning,omitempty"`
	ConfigPath    string               `json:"config_path"`
	DataPath      string               `json:"data_path"`
	MemoryPath    string               `json:"memory_path"`
	StatePath     string               `json:"state_path"`
	WorkspacePath string               `json:"workspace_path"`
	Actors        []actorRuntimeStatus `json:"actors"`
}

// taskInspectEvent is the JSON-safe mirror of bridge.StreamEvent the frontend
// renders in the pop-up. We project only the fields the UI needs so the wails
// binding stays stable across bridge changes.
type taskInspectEvent struct {
	Kind             string `json:"kind"`
	Delta            string `json:"delta,omitempty"`
	ToolName         string `json:"tool_name,omitempty"`
	ToolID           string `json:"tool_id,omitempty"`
	ToolArguments    string `json:"tool_arguments,omitempty"`
	ToolResult       string `json:"tool_result,omitempty"`
	Status           string `json:"status,omitempty"`
	TaskStatus       string `json:"task_status,omitempty"`
	Summary          string `json:"summary,omitempty"`
	Error            string `json:"error,omitempty"`
	CheckpointIndex  int    `json:"checkpoint_index,omitempty"`
	CheckpointReason string `json:"checkpoint_reason,omitempty"`
	At               string `json:"at"`
}

// taskInspectResult mirrors orchestrator.TaskInspectResult for the wails
// binding. The Events slice is the progress tail (newest last); EventCount is
// the total line count on disk so the frontend can request the next slice.
type taskInspectResult struct {
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
	EventCount int                      `json:"event_count"`
	UpdatedAt  string                   `json:"updated_at"`
}

func sendChat(socketPath, sessionID, message string) (chatResult, error) {
	return sendChatWithStatus(socketPath, sessionID, message, nil)
}

func sendChatWithStatus(socketPath, sessionID, message string, onEvent func(bridge.StreamEvent)) (chatResult, error) {
	var result chatResult
	responses, err := roundTripWithEvent(socketPath, ipcRequest{Op: "chat_send", SessionID: sessionID, Message: message}, func(res ipcResponse) {
		if onEvent != nil && res.Event != nil {
			onEvent(*res.Event)
		}
	})
	if err != nil {
		return result, err
	}
	for _, res := range responses {
		if !res.OK {
			return result, fmt.Errorf("core error: %s", res.Message)
		}
		if res.SessionID != "" {
			result.SessionID = res.SessionID
		}
		if res.Usage != nil {
			result.Usage = mapUsage(res.Usage)
		}
		if res.Event != nil {
			ev := *res.Event
			if ev.Kind == bridge.EventTranscript && ev.Transcript != nil {
				if ev.GenerationID != "" {
					result.GenerationID = ev.GenerationID
				}
				result.Transcript = ev.Transcript.Entries
			}
		}
	}
	result.OK = true
	return result, nil
}

func chatHistory(socketPath string) (chatHistoryResult, error) {
	var result chatHistoryResult
	responses, err := roundTrip(socketPath, ipcRequest{Op: "chat_history"})
	if err != nil {
		return result, err
	}
	if len(responses) == 0 || !responses[0].OK {
		return result, fmt.Errorf("core error")
	}
	res := responses[0]
	result.OK = true
	result.SessionID = res.SessionID
	result.Usage = mapUsage(res.Usage)
	result.Transcript = res.Transcript
	return result, nil
}

func listSessions(socketPath string) (sessionListResult, error) {
	var result sessionListResult
	responses, err := roundTrip(socketPath, ipcRequest{Op: "session_list"})
	if err != nil {
		return result, err
	}
	if len(responses) == 0 || !responses[0].OK {
		message := "core error"
		if len(responses) > 0 && responses[0].Message != "" {
			message = responses[0].Message
		}
		return result, fmt.Errorf("%s", message)
	}
	res := responses[0]
	result.OK = true
	for _, item := range res.Sessions {
		result.Sessions = append(result.Sessions, sessionSummary{
			ID: item.ID, Title: item.Title, Active: item.Active,
			TurnCount: item.TurnCount, UpdatedAt: item.UpdatedAt, CreatedAt: item.CreatedAt,
		})
	}
	return result, nil
}

func switchSession(socketPath, sessionID string) (string, error) {
	responses, err := roundTrip(socketPath, ipcRequest{Op: "session_switch", SessionID: sessionID})
	if err != nil {
		return "", err
	}
	if len(responses) == 0 || !responses[0].OK {
		message := "core error"
		if len(responses) > 0 && responses[0].Message != "" {
			message = responses[0].Message
		}
		return "", fmt.Errorf("%s", message)
	}
	return responses[0].SessionID, nil
}

func newSession(socketPath string) (string, error) {
	responses, err := roundTrip(socketPath, ipcRequest{Op: "session_new"})
	if err != nil {
		return "", err
	}
	if len(responses) == 0 || !responses[0].OK {
		message := "core error"
		if len(responses) > 0 && responses[0].Message != "" {
			message = responses[0].Message
		}
		return "", fmt.Errorf("%s", message)
	}
	return responses[0].SessionID, nil
}

func deleteChatTurn(socketPath, sessionID string, turnID int64) error {
	responses, err := roundTrip(socketPath, ipcRequest{Op: "chat_delete", SessionID: sessionID, TurnID: turnID})
	if err != nil {
		return err
	}
	if len(responses) == 0 || !responses[0].OK {
		message := "core error"
		if len(responses) > 0 && responses[0].Message != "" {
			message = responses[0].Message
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func retryChatTurnWithStatus(socketPath, sessionID string, turnID int64, onEvent func(bridge.StreamEvent)) (chatResult, error) {
	var result chatResult
	responses, err := roundTripWithEvent(socketPath, ipcRequest{Op: "chat_retry", SessionID: sessionID, TurnID: turnID}, func(res ipcResponse) {
		if onEvent != nil && res.Event != nil {
			onEvent(*res.Event)
		}
	})
	if err != nil {
		return result, err
	}
	for _, res := range responses {
		if !res.OK {
			return result, fmt.Errorf("core error: %s", res.Message)
		}
		if res.SessionID != "" {
			result.SessionID = res.SessionID
		}
		if res.Usage != nil {
			result.Usage = mapUsage(res.Usage)
		}
		if res.Event != nil {
			ev := *res.Event
			if ev.Kind == bridge.EventTranscript && ev.Transcript != nil {
				if ev.GenerationID != "" {
					result.GenerationID = ev.GenerationID
				}
				result.Transcript = ev.Transcript.Entries
			}
		}
	}
	result.OK = true
	return result, nil
}

func submitFeedback(socketPath, sessionID string, turnID int64, signal, correction string) error {
	responses, err := roundTrip(socketPath, ipcRequest{Op: "submit_feedback", SessionID: sessionID, TurnID: turnID, Signal: signal, Correction: correction})
	if err != nil {
		return err
	}
	if len(responses) == 0 || !responses[0].OK {
		message := "core error"
		if len(responses) > 0 && responses[0].Message != "" {
			message = responses[0].Message
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func stopChat(socketPath, sessionID string) error {
	responses, err := roundTrip(socketPath, ipcRequest{Op: "chat_stop", SessionID: sessionID, Scope: "generation"})
	if err != nil {
		return err
	}
	if len(responses) == 0 || !responses[0].OK {
		return fmt.Errorf("core error")
	}
	return nil
}

func contextUsage(socketPath string) (*chatUsage, error) {
	responses, err := roundTrip(socketPath, ipcRequest{Op: "context_usage"})
	if err != nil {
		return nil, err
	}
	if len(responses) == 0 || !responses[0].OK {
		return nil, fmt.Errorf("core error")
	}
	return mapUsage(responses[0].Usage), nil
}

func runtimeInfo(socketPath string) (*runtimeStatus, error) {
	responses, err := roundTrip(socketPath, ipcRequest{Op: "runtime_status"})
	if err != nil {
		return nil, err
	}
	if len(responses) == 0 || !responses[0].OK || responses[0].Runtime == nil {
		return nil, fmt.Errorf("core error")
	}
	src := responses[0].Runtime
	out := &runtimeStatus{
		Provider: src.Provider, Model: src.Model, Driver: src.Driver,
		Reasoning: src.Reasoning, ConfigPath: src.ConfigPath, DataPath: src.DataPath,
		MemoryPath: src.MemoryPath, StatePath: src.StatePath, WorkspacePath: src.WorkspacePath,
	}
	for _, actor := range src.Actors {
		out.Actors = append(out.Actors, actorRuntimeStatus{
			ID: actor.ID, Role: actor.Role, Status: actor.Status,
			Phase: actor.Phase, Workspace: actor.Workspace,
		})
	}
	return out, nil
}

func mapUsage(usage *chatstore.Usage) *chatUsage {
	if usage == nil {
		return nil
	}
	return &chatUsage{
		SessionID:      usage.SessionID,
		UsedTokens:     usage.UsedTokens,
		ContextWindow:  usage.ContextWindow,
		Percent:        usage.Percent,
		Provider:       usage.Provider,
		Model:          usage.Model,
		CompactedTurns: usage.CompactedTurns,
		ActiveTurns:    usage.ActiveTurns,
	}
}

// taskInspect fetches the durable task record + a tail of its progress stream
// for the widget's "Planner & Agent" pop-up. afterLine is the number of
// progress lines the caller has already seen (0 on first open).
func taskInspect(socketPath, taskID string, afterLine int) (*taskInspectResult, error) {
	responses, err := roundTrip(socketPath, ipcRequest{Op: "task_inspect", TaskID: taskID, AfterLine: afterLine})
	if err != nil {
		return nil, err
	}
	if len(responses) == 0 || !responses[0].OK {
		msg := "core error"
		if len(responses) > 0 {
			msg = responses[0].Message
		}
		return nil, fmt.Errorf("%s", msg)
	}
	src := responses[0].TaskInspect
	if src == nil {
		return nil, fmt.Errorf("core error")
	}
	out := &taskInspectResult{
		ID: src.ID, Role: src.Role, Status: src.Status, Task: src.Task,
		Result: src.Result, Error: src.Error, Question: src.Question,
		PlanTaskID: src.PlanTaskID, Plan: src.Plan,
		Transcript: src.Transcript,
		EventCount: src.EventCount,
		UpdatedAt:  src.UpdatedAt.Format(time.RFC3339Nano),
	}
	return out, nil
}

func slashSuggest(socketPath, query string) ([]config.CommandEntry, error) {
	responses, err := roundTrip(socketPath, ipcRequest{Op: "slash_suggest", Query: query})
	if err != nil {
		return nil, err
	}
	if len(responses) == 0 || !responses[0].OK {
		return nil, fmt.Errorf("core error")
	}
	return responses[0].Suggestions, nil
}

func pingCore(socketPath string) (pingResult, error) {
	start := time.Now()
	responses, err := roundTrip(socketPath, ipcRequest{Op: "ping"})
	if err != nil {
		return pingResult{}, err
	}
	if len(responses) == 0 {
		return pingResult{}, fmt.Errorf("no response")
	}
	res := responses[0]
	if !res.OK {
		return pingResult{}, fmt.Errorf("core error: %s", res.Message)
	}

	return pingResult{
		OK:          true,
		Message:     res.Message,
		RingState:   res.RingState,
		ServerMs:    res.ServerMs,
		RoundTripMs: time.Since(start).Milliseconds(),
		SocketPath:  socketPath,
	}, nil
}

func roundTrip(socketPath string, req ipcRequest) ([]ipcResponse, error) {
	return roundTripWithEvent(socketPath, req, nil)
}

// watchEvents opens a long-lived `watch` subscription to the core event bus and
// invokes onEvent for every pushed StreamEvent. It blocks, reconnecting with a
// short backoff if the connection drops, until stop is closed. This is the
// async channel that carries background sub-agent completion (EventTaskUpdate)
// to the widget without an active chat request.
func watchEvents(socketPath string, stop <-chan struct{}, onEvent func(bridge.StreamEvent)) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
		if err != nil {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}
		func() {
			defer conn.Close()
			// Scanner.Scan blocks while the watch socket is idle. Tie the
			// connection lifetime to stop so shutdown closes the fd and wakes
			// Scan immediately instead of waiting for another event.
			watchDone := make(chan struct{})
			defer close(watchDone)
			go func() {
				select {
				case <-stop:
					_ = conn.Close()
				case <-watchDone:
				}
			}()
			b, _ := json.Marshal(ipcRequest{Op: "watch"})
			if _, werr := conn.Write(append(b, '\n')); werr != nil {
				return
			}
			sc := bufio.NewScanner(conn)
			sc.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
			for sc.Scan() {
				select {
				case <-stop:
					return
				default:
				}
				var res ipcResponse
				if jerr := json.Unmarshal(sc.Bytes(), &res); jerr != nil {
					continue
				}
				if res.Op == "event" && res.Event != nil && onEvent != nil {
					onEvent(*res.Event)
				}
			}
		}()
		select {
		case <-stop:
			return
		case <-time.After(1 * time.Second):
		}
	}
}

func roundTripWithEvent(socketPath string, req ipcRequest, onResponse func(ipcResponse)) ([]ipcResponse, error) {
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", socketPath, err)
	}
	defer conn.Close()

	deadline := 3 * time.Second
	if req.Op == "chat_send" || req.Op == "chat_retry" {
		deadline = 35 * time.Minute
	}
	if err := conn.SetDeadline(time.Now().Add(deadline)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	sc := bufio.NewScanner(conn)
	// Responses can echo large attachment payloads (e.g. chat_history turns with
	// inlined images/files), which exceed bufio.Scanner's default 64KB line cap.
	sc.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
	var responses []ipcResponse
scanLoop:
	for sc.Scan() {
		var res ipcResponse
		if err := json.Unmarshal(sc.Bytes(), &res); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		responses = append(responses, res)
		if onResponse != nil {
			onResponse(res)
		}
		if req.Op != "chat_send" && req.Op != "chat_retry" {
			break scanLoop
		}
		if res.Op == "event" && res.Event != nil {
			switch res.Event.Kind {
			case bridge.EventDone, bridge.EventError:
				break scanLoop
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return responses, nil
}
