package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/jahrulnr/sapaloq/internal/core/orchestrator"
)

type Server struct {
	cfg  config.Config
	orch *orchestrator.Orchestrator
}

func NewServer(cfg config.Config, orch *orchestrator.Orchestrator) *Server {
	return &Server{cfg: cfg, orch: orch}
}

func (s *Server) ListenAndServe(ctx context.Context, socketPath string) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer ln.Close()
	defer os.Remove(socketPath)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		start := time.Now()
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			write(conn, Response{OK: false, Message: "invalid json"})
			continue
		}
		switch req.Op {
		case "ping":
			write(conn, Response{OK: true, Op: "ping", Message: "pong", RingState: string(orchestrator.RingIdle), ServerMs: time.Since(start).Milliseconds()})
		case "slash_suggest":
			write(conn, Response{OK: true, Op: req.Op, Suggestions: s.cfg.Commands.Suggest(req.Query), ServerMs: time.Since(start).Milliseconds()})
		case "chat_send":
			s.handleChat(ctx, conn, req, start)
		case "watch":
			s.handleWatch(ctx, conn)
		default:
			write(conn, Response{OK: false, Op: req.Op, Message: "unknown op", ServerMs: time.Since(start).Milliseconds()})
		}
	}
}

func (s *Server) handleChat(ctx context.Context, conn net.Conn, req Request, start time.Time) {
	write(conn, Response{OK: true, Op: "chat_send", Message: "accepted", SessionID: req.SessionID, RingState: string(orchestrator.RingThinking), ServerMs: time.Since(start).Milliseconds()})
	stream, err := s.orch.SendChat(ctx, req.SessionID, req.Message)
	if err != nil {
		write(conn, Response{OK: false, Op: "event", Message: err.Error()})
		return
	}
	for ev := range stream {
		ring := orchestrator.RingStateFor(ev.Kind)
		write(conn, Response{OK: true, Op: "event", Event: &ev, RingState: string(ring)})
		if ring != orchestrator.RingIdle {
			write(conn, Response{OK: true, Op: "ring_state", RingState: string(ring)})
		}
	}
}

func (s *Server) handleWatch(ctx context.Context, conn net.Conn) {
	events, cancel := s.orch.Bus().Subscribe(64)
	defer cancel()
	write(conn, Response{OK: true, Op: "watch", Message: "subscribed"})
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			data := ev.Data
			write(conn, Response{OK: true, Op: "event", Event: &data, RingState: string(orchestrator.RingStateFor(data.Kind))})
		}
	}
}

func write(conn net.Conn, res Response) {
	b, _ := json.Marshal(res)
	_, _ = conn.Write(append(b, '\n'))
}
