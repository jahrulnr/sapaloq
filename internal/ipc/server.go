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

// maxFrameBytes caps a single newline-delimited IPC frame. It must comfortably
// exceed the widget's 8 MB attachment limit after base64 inflation (~11 MB)
// plus JSON overhead.
const maxFrameBytes = 16 * 1024 * 1024

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
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	sc := bufio.NewScanner(conn)
	// Requests can carry inlined attachments (base64 images / file text), which
	// easily exceed bufio.Scanner's default 64KB line cap and would otherwise
	// abort the read mid-message (manifesting as a "broken pipe" on the client).
	sc.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
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
		case "session_list":
			s.handleSessionList(ctx, conn, req, start)
		case "session_switch":
			s.handleSessionSwitch(ctx, conn, req, start)
		case "session_new":
			s.handleSessionNew(ctx, conn, req, start)
		case "context_usage":
			s.handleUsage(ctx, conn, req, start)
		case "runtime_status":
			status := s.orch.RuntimeStatus()
			write(conn, Response{OK: true, Op: req.Op, Runtime: &status, ServerMs: time.Since(start).Milliseconds()})
		case "task_inspect":
			inspect, err := s.orch.TaskInspect(req.TaskID, req.AfterLine)
			if err != nil {
				write(conn, Response{OK: false, Op: req.Op, Message: err.Error(), ServerMs: time.Since(start).Milliseconds()})
				continue
			}
			write(conn, Response{OK: true, Op: req.Op, TaskInspect: &inspect, ServerMs: time.Since(start).Milliseconds()})
		case "chat_delete":
			s.handleDelete(ctx, conn, req, start)
		case "chat_retry":
			s.handleRetry(ctx, conn, req, start)
		case "chat_stop":
			_, message := s.orch.Stop(req.SessionID, req.Scope, req.TaskID)
			write(conn, Response{OK: true, Op: req.Op, Message: message, SessionID: req.SessionID, ServerMs: time.Since(start).Milliseconds()})
		case "chat_send":
			s.handleChat(ctx, conn, req, start)
		case "submit_feedback":
			s.handleFeedback(ctx, conn, req, start)
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

func (s *Server) handleFeedback(ctx context.Context, conn net.Conn, req Request, start time.Time) {
	if err := s.orch.SubmitFeedback(ctx, req.SessionID, req.TurnID, req.Signal, req.Correction); err != nil {
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
		resp := Response{OK: true, Op: "event", SessionID: sessionID, Event: &ev, RingState: string(ring)}
		// Usage requires a SQLite query; only attach it on terminal events so
		// per-delta streaming stays fast (querying every token stalls the
		// stream and makes it arrive in bursts).
		if ev.Kind == bridge.EventDone || ev.Kind == bridge.EventError {
			usage, _ := s.orch.ContextUsage(ctx, sessionID)
			resp.Usage = &usage
		}
		if err := write(conn, resp); err != nil {
			return
		}
		if ring != orchestrator.RingIdle {
			if err := write(conn, Response{OK: true, Op: "ring_state", SessionID: sessionID, RingState: string(ring)}); err != nil {
				return
			}
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
	timeline := s.orch.SessionTimeline(sessionID)
	write(conn, Response{OK: true, Op: req.Op, SessionID: sessionID, Turns: turns, Timeline: timeline, Usage: &usage, ServerMs: time.Since(start).Milliseconds()})
}

func (s *Server) handleSessionList(ctx context.Context, conn net.Conn, req Request, start time.Time) {
	sessions, err := s.orch.ListSessions(ctx, 50)
	if err != nil {
		write(conn, Response{OK: false, Op: req.Op, Message: err.Error(), ServerMs: time.Since(start).Milliseconds()})
		return
	}
	write(conn, Response{OK: true, Op: req.Op, Sessions: sessions, ServerMs: time.Since(start).Milliseconds()})
}

func (s *Server) handleSessionSwitch(ctx context.Context, conn net.Conn, req Request, start time.Time) {
	if req.SessionID == "" {
		write(conn, Response{OK: false, Op: req.Op, Message: "session_id is required", ServerMs: time.Since(start).Milliseconds()})
		return
	}
	sessionID, err := s.orch.SwitchSession(ctx, req.SessionID)
	if err != nil {
		write(conn, Response{OK: false, Op: req.Op, Message: err.Error(), ServerMs: time.Since(start).Milliseconds()})
		return
	}
	write(conn, Response{OK: true, Op: req.Op, SessionID: sessionID, ServerMs: time.Since(start).Milliseconds()})
}

func (s *Server) handleSessionNew(ctx context.Context, conn net.Conn, req Request, start time.Time) {
	sessionID, err := s.orch.NewSession(ctx)
	if err != nil {
		write(conn, Response{OK: false, Op: req.Op, Message: err.Error(), ServerMs: time.Since(start).Milliseconds()})
		return
	}
	write(conn, Response{OK: true, Op: req.Op, SessionID: sessionID, ServerMs: time.Since(start).Milliseconds()})
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
	if err := write(conn, Response{OK: true, Op: "watch", Message: "subscribed"}); err != nil {
		return
	}
	// Durable catch-up: a widget may start late or reconnect after the in-memory
	// bus push. Rehydrate recent task states from status.json before streaming
	// live events. The frontend updates one card per task id, so overlap with a
	// queued live event is harmless.
	for _, event := range s.orch.RecentTaskUpdates(20) {
		ev := event
		if err := write(conn, Response{
			OK:        true,
			Op:        "event",
			SessionID: ev.SessionID,
			Event:     &ev,
			RingState: string(orchestrator.RingStateFor(ev.Kind)),
		}); err != nil {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			data := ev.Data
			if err := write(conn, Response{OK: true, Op: "event", Event: &data, RingState: string(orchestrator.RingStateFor(data.Kind))}); err != nil {
				return
			}
		}
	}
}

func write(conn net.Conn, res Response) error {
	b, _ := json.Marshal(res)
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	_, err := conn.Write(append(b, '\n'))
	return err
}
