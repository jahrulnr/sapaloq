package main

import (
	"context"
	"os"
)

// App exposes Go methods to the Wails frontend.
type App struct {
	ctx        context.Context
	socketPath string
}

func NewApp() *App {
	return &App{socketPath: defaultSocketPath()}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if p := os.Getenv("SAPALOQ_SOCKET"); p != "" {
		a.socketPath = p
	}
}

// PingCore round-trips a ping to the mock sapaloq-core unix socket.
func (a *App) PingCore() (pingResult, error) {
	return pingCore(a.socketPath)
}

// SocketPath returns the path used for IPC (for UI display).
func (a *App) SocketPath() string {
	return a.socketPath
}

// SyncInputShape updates GTK input region (Linux: circle when collapsed).
func (a *App) SyncInputShape(collapsed bool) {
	scheduleInputShape(collapsed)
}
