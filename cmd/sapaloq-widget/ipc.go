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
	return filepath.Join(os.Getenv("HOME"), ".config", "sapaloq", "run", "sapaloq.sock")
}

type chatResult struct {
	OK        bool                 `json:"ok"`
	SessionID string               `json:"session_id,omitempty"`
	Events    []bridge.StreamEvent `json:"events"`
	Usage     *chatUsage           `json:"usage,omitempty"`
}

type chatTurn struct {
	ID      int64  `json:"id"`
	Seq     int    `json:"seq"`
	Role    string `json:"role"`
	Content string `json:"content"`
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
	OK        bool       `json:"ok"`
	SessionID string     `json:"session_id"`
	Turns     []chatTurn `json:"turns"`
	Usage     *chatUsage `json:"usage,omitempty"`
}

func sendChat(socketPath, sessionID, message string) (chatResult, error) {
	var result chatResult
	responses, err := roundTrip(socketPath, ipcRequest{Op: "chat_send", SessionID: sessionID, Message: message})
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
			result.Events = append(result.Events, *res.Event)
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
	for _, turn := range res.Turns {
		if turn.Role == "system" {
			continue
		}
		result.Turns = append(result.Turns, chatTurn{ID: turn.ID, Seq: turn.Seq, Role: turn.Role, Content: turn.Content})
	}
	return result, nil
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

func retryChatTurn(socketPath, sessionID string, turnID int64) (chatResult, error) {
	var result chatResult
	responses, err := roundTrip(socketPath, ipcRequest{Op: "chat_retry", SessionID: sessionID, TurnID: turnID})
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
			result.Events = append(result.Events, *res.Event)
		}
	}
	result.OK = true
	return result, nil
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
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", socketPath, err)
	}
	defer conn.Close()

	deadline := 3 * time.Second
	if req.Op == "chat_send" {
		deadline = 5 * time.Minute
	}
	if err := conn.SetDeadline(time.Now().Add(deadline)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	sc := bufio.NewScanner(conn)
	var responses []ipcResponse
	for sc.Scan() {
		var res ipcResponse
		if err := json.Unmarshal(sc.Bytes(), &res); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		responses = append(responses, res)
		if req.Op != "chat_send" || res.Op == "event" && res.Event != nil && res.Event.Kind == bridge.EventDone {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return responses, nil
}
