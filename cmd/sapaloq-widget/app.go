package main

import (
	"context"
	"os"

	"github.com/jahrulnr/sapaloq/internal/config"
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

// PingCore round-trips a ping to sapaloq-core unix socket.
func (a *App) PingCore() (pingResult, error) {
	return pingCore(a.socketPath)
}

func (a *App) SendMessage(sessionID string, text string) (chatResult, error) {
	return sendChat(a.socketPath, sessionID, text)
}

func (a *App) ChatHistory() (chatHistoryResult, error) {
	return chatHistory(a.socketPath)
}

func (a *App) ContextUsage() (*chatUsage, error) {
	return contextUsage(a.socketPath)
}

func (a *App) SlashSuggest(query string) ([]config.CommandEntry, error) {
	return slashSuggest(a.socketPath, query)
}

// SocketPath returns the path used for IPC (for UI display).
func (a *App) SocketPath() string {
	return a.socketPath
}

// SyncInputShape updates GTK input region (Linux: circle when collapsed).
func (a *App) SyncInputShape(collapsed bool) {
	scheduleInputShape(collapsed)
}
