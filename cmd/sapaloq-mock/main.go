// Mock sapaloq-core unix socket for M5a IPC spike.
// Protocol: one JSON object per line (newline-delimited).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
)

type request struct {
	Op string `json:"op"`
}

type response struct {
	OK        bool   `json:"ok"`
	Op        string `json:"op"`
	Message   string `json:"message,omitempty"`
	RingState string `json:"ring_state,omitempty"`
	ServerMs  int64  `json:"server_ms"`
}

func defaultMockSocket() string {
	if p, err := config.RepoMockSocketPath(); err == nil {
		return p
	}
	return filepath.Join(".sapaloq", "run", "sapaloq-mock.sock")
}

func main() {
	socketPath := flag.String("socket", defaultMockSocket(), "unix socket path (default: <repo>/.sapaloq/run/sapaloq-mock.sock)")
	flag.Parse()

	abs, err := filepath.Abs(*socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "socket path: %v\n", err)
		os.Exit(1)
	}
	if config.IsProductionSocketPath(abs) {
		fmt.Fprintf(os.Stderr, "refusing to bind production socket %q; use repo mock path or -socket\n", abs)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
	_ = os.Remove(abs)

	ln, err := net.Listen("unix", abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()
	defer os.Remove(abs)

	fmt.Printf("mock-core listening on %s\n", abs)

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go handle(conn)
	}
}

func handle(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		start := time.Now()
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			write(conn, response{OK: false, Message: "invalid json"})
			continue
		}
		switch req.Op {
		case "ping":
			write(conn, response{
				OK:        true,
				Op:        "ping",
				Message:   "pong",
				RingState: "idle",
				ServerMs:  time.Since(start).Milliseconds(),
			})
		case "ring":
			write(conn, response{
				OK:        true,
				Op:        "ring",
				RingState: "thinking",
				ServerMs:  time.Since(start).Milliseconds(),
			})
		default:
			write(conn, response{OK: false, Message: "unknown op"})
		}
	}
}

func write(conn net.Conn, res response) {
	b, _ := json.Marshal(res)
	b = append(b, '\n')
	_, _ = conn.Write(b)
}
