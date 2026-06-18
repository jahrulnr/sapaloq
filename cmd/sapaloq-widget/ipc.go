package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

type ipcRequest struct {
	Op string `json:"op"`
}

type ipcResponse struct {
	OK        bool   `json:"ok"`
	Op        string `json:"op"`
	Message   string `json:"message,omitempty"`
	RingState string `json:"ring_state,omitempty"`
	ServerMs  int64  `json:"server_ms"`
}

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
	return filepath.Join(os.TempDir(), "sapaloq-spike.sock")
}

func pingCore(socketPath string) (pingResult, error) {
	start := time.Now()
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err != nil {
		return pingResult{}, fmt.Errorf("dial %s: %w", socketPath, err)
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	if err := conn.SetDeadline(deadline); err != nil {
		return pingResult{}, fmt.Errorf("set deadline: %w", err)
	}

	req, _ := json.Marshal(ipcRequest{Op: "ping"})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return pingResult{}, fmt.Errorf("write: %w", err)
	}

	sc := bufio.NewScanner(conn)
	if !sc.Scan() {
		return pingResult{}, fmt.Errorf("no response")
	}

	var res ipcResponse
	if err := json.Unmarshal(sc.Bytes(), &res); err != nil {
		return pingResult{}, fmt.Errorf("decode: %w", err)
	}
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
