package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
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
			write(conn, Response{OK: true, Op: req.Op, Suggestions: s.orch.SlashSuggest(req.Query), ServerMs: time.Since(start).Milliseconds()})
		case "session_active", "chat_history":
			s.handleHistory(ctx, conn, req, start)
		case "context_usage":
			s.handleUsage(ctx, conn, req, start)
		case "chat_delete":
			s.handleDelete(ctx, conn, req, start)
		case "chat_retry":
			s.handleRetry(ctx, conn, req, start)
		case "chat_stop":
			_, message := s.orch.Stop(req.SessionID, req.Scope, req.TaskID)
			write(conn, Response{OK: true, Op: req.Op, Message: message, SessionID: req.SessionID, ServerMs: time.Since(start).Milliseconds()})
		case "chat_send":
			s.handleChat(ctx, conn, req, start)
		case "watch":
			s.handleWatch(ctx, conn)
		default:
			write(conn, Response{OK: false, Op: req.Op, Message: "unknown op", ServerMs: time.Since(start).Milliseconds()})
		}
	}
}

func (s *Server) handleDelete(ctx context.Context, conn net.Conn, req Request, start time.Time) {
	if req.TurnID <= 0 {
		write(conn, Response{OK: false, Op: req.Op, Message: "turn_id is required", ServerMs: time.Since(start).Milliseconds()})
		return
	}
	if err := s.orch.DeleteTurn(ctx, req.SessionID, req.TurnID); err != nil {
		write(conn, Response{OK: false, Op: req.Op, Message: err.Error(), ServerMs: time.Since(start).Milliseconds()})
		return
	}
	write(conn, Response{OK: true, Op: req.Op, SessionID: req.SessionID, ServerMs: time.Since(start).Milliseconds()})
}

func (s *Server) handleRetry(ctx context.Context, conn net.Conn, req Request, start time.Time) {
	stream, err := s.orch.RetryChat(ctx, req.SessionID, req.TurnID)
	if err != nil {
		write(conn, Response{OK: false, Op: req.Op, Message: err.Error(), ServerMs: time.Since(start).Milliseconds()})
		return
	}
	write(conn, Response{OK: true, Op: req.Op, SessionID: req.SessionID, RingState: string(orchestrator.RingThinking), ServerMs: time.Since(start).Milliseconds()})
	s.writeStream(ctx, conn, req.SessionID, stream)
}

func (s *Server) handleChat(ctx context.Context, conn net.Conn, req Request, start time.Time) {
	stream, err := s.orch.SendChat(ctx, req.SessionID, req.Message)
	if err != nil {
		write(conn, Response{OK: false, Op: "event", Message: err.Error()})
		return
	}
	write(conn, Response{OK: true, Op: "chat_send", Message: "accepted", SessionID: req.SessionID, RingState: string(orchestrator.RingThinking), ServerMs: time.Since(start).Milliseconds()})
	s.writeStream(ctx, conn, req.SessionID, stream)
}

func (s *Server) writeStream(ctx context.Context, conn net.Conn, requestedSessionID string, stream <-chan bridge.StreamEvent) {
	sessionID := requestedSessionID
	for ev := range stream {
		if ev.SessionID != "" {
			sessionID = ev.SessionID
		}
		ring := orchestrator.RingStateFor(ev.Kind)
		usage, _ := s.orch.ContextUsage(ctx, sessionID)
		write(conn, Response{OK: true, Op: "event", SessionID: sessionID, Event: &ev, RingState: string(ring), Usage: &usage})
		if ring != orchestrator.RingIdle {
			write(conn, Response{OK: true, Op: "ring_state", SessionID: sessionID, RingState: string(ring)})
		}
	}
}

func (s *Server) handleHistory(ctx context.Context, conn net.Conn, req Request, start time.Time) {
	sessionID := req.SessionID
	if sessionID == "" {
		var err error
		sessionID, err = s.orch.ActiveSession(ctx)
		if err != nil {
			write(conn, Response{OK: false, Op: req.Op, Message: err.Error(), ServerMs: time.Since(start).Milliseconds()})
			return
		}
	}
	turns, err := s.orch.ActiveTurns(ctx, sessionID)
	if err != nil {
		write(conn, Response{OK: false, Op: req.Op, Message: err.Error(), ServerMs: time.Since(start).Milliseconds()})
		return
	}
	usage, _ := s.orch.ContextUsage(ctx, sessionID)
	write(conn, Response{OK: true, Op: req.Op, SessionID: sessionID, Turns: turns, Usage: &usage, ServerMs: time.Since(start).Milliseconds()})
}

func (s *Server) handleUsage(ctx context.Context, conn net.Conn, req Request, start time.Time) {
	usage, err := s.orch.ContextUsage(ctx, req.SessionID)
	if err != nil {
		write(conn, Response{OK: false, Op: req.Op, Message: err.Error(), ServerMs: time.Since(start).Milliseconds()})
		return
	}
	write(conn, Response{OK: true, Op: req.Op, SessionID: usage.SessionID, Usage: &usage, ServerMs: time.Since(start).Milliseconds()})
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
