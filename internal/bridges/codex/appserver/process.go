package appserver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/sapaloq/internal/debug"
)

const (
	ModeAuto     = "auto"
	ModeExternal = "external"
	ModeManaged  = "managed"
)

type Manager struct {
	Binary   string
	Endpoint string
	Mode     string
	Env      []string

	mu          sync.Mutex
	cmd         *exec.Cmd
	waitDone    chan struct{}
	waitErr     error
	spawnedByUs bool
}

func (m *Manager) EnsureRunning(ctx context.Context) error {
	if err := Probe(ctx, m.Endpoint); err == nil {
		return nil
	} else if m.Mode == ModeExternal || m.Mode == ModeManaged {
		return fmt.Errorf("codex app-server unavailable at %s (%s mode): %w", m.Endpoint, m.Mode, err)
	}

	m.mu.Lock()
	if m.cmd != nil {
		done := m.waitDone
		m.mu.Unlock()
		return m.waitReady(ctx, done)
	}
	if strings.HasPrefix(m.Endpoint, "unix://") {
		path := strings.TrimPrefix(m.Endpoint, "unix://")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("create app-server socket directory: %w", err)
		}
	}
	cmd := exec.Command(m.Binary, "app-server", "--listen", m.Endpoint)
	cmd.Env = m.Env
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("start codex app-server: %w", err)
	}
	done := make(chan struct{})
	m.cmd = cmd
	m.waitDone = done
	m.waitErr = nil
	m.spawnedByUs = true
	m.mu.Unlock()
	debug.Debugf("codex-bridge: spawned app-server pid=%d endpoint=%s", cmd.Process.Pid, m.Endpoint)
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		m.waitErr = err
		m.mu.Unlock()
		close(done)
	}()
	return m.waitReady(ctx, done)
}

func (m *Manager) waitReady(ctx context.Context, done <-chan struct{}) error {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		lastErr = Probe(probeCtx, m.Endpoint)
		cancel()
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			m.mu.Lock()
			err := m.waitErr
			m.cmd = nil
			m.waitDone = nil
			m.waitErr = nil
			m.spawnedByUs = false
			m.mu.Unlock()
			return fmt.Errorf("codex app-server exited before ready: %w", err)
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for codex app-server at %s: %w", m.Endpoint, lastErr)
		case <-ticker.C:
		}
	}
}

func (m *Manager) SpawnedByUs() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spawnedByUs
}

func (m *Manager) Close() error {
	m.mu.Lock()
	cmd := m.cmd
	done := m.waitDone
	spawned := m.spawnedByUs
	m.cmd = nil
	m.waitDone = nil
	m.waitErr = nil
	m.spawnedByUs = false
	m.mu.Unlock()
	if !spawned || cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := terminateProcessGroup(cmd); err != nil {
		debug.Debugf("codex-bridge: app-server terminate failed: %v", err)
	}
	select {
	case <-done:
		return nil
	case <-time.After(3 * time.Second):
		_ = killProcessGroup(cmd)
		select {
		case <-done:
			return nil
		case <-time.After(time.Second):
			return fmt.Errorf("timed out reaping codex app-server pid %d", cmd.Process.Pid)
		}
	}
}

func Probe(ctx context.Context, endpoint string) error {
	c, err := Dial(ctx, endpoint, nil)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Initialize(ctx)
}

type AuthStatus struct {
	AuthMethod         *string `json:"authMethod"`
	RequiresOpenAIAuth *bool   `json:"requiresOpenaiAuth"`
}

func ProbeAuth(ctx context.Context, endpoint string) (AuthStatus, error) {
	c, err := Dial(ctx, endpoint, nil)
	if err != nil {
		return AuthStatus{}, err
	}
	defer c.Close()
	if err := c.Initialize(ctx); err != nil {
		return AuthStatus{}, err
	}
	var status AuthStatus
	err = c.Call(ctx, "getAuthStatus", map[string]any{"includeToken": false, "refreshToken": false}, &status)
	return status, err
}
