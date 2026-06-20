package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
	"github.com/wailsapp/wails/v2/pkg/runtime"
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
	return sendChatWithStatus(a.socketPath, sessionID, text, func(event bridge.StreamEvent) {
		// Forward every stream event to the webview as it arrives so deltas
		// render live instead of bursting when the call resolves.
		runtime.EventsEmit(a.ctx, "sapaloq:stream", event)
	})
}

func (a *App) ChatHistory() (chatHistoryResult, error) {
	return chatHistory(a.socketPath)
}

func (a *App) DeleteChatTurn(sessionID string, turnID int64) error {
	return deleteChatTurn(a.socketPath, sessionID, turnID)
}

func (a *App) RetryChatTurn(sessionID string, turnID int64) (chatResult, error) {
	return retryChatTurnWithStatus(a.socketPath, sessionID, turnID, func(event bridge.StreamEvent) {
		runtime.EventsEmit(a.ctx, "sapaloq:stream", event)
	})
}

func (a *App) StopChat(sessionID string) error {
	return stopChat(a.socketPath, sessionID)
}

// SubmitFeedback records an explicit 👍/👎 reward for an assistant turn. signal
// is "up" or "down"; correction is an optional note (used on 👎) that becomes
// negative guidance for future turns.
func (a *App) SubmitFeedback(sessionID string, turnID int64, signal string, correction string) error {
	return submitFeedback(a.socketPath, sessionID, turnID, signal, correction)
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

// droppedFile is the payload returned by ReadDroppedFile for the frontend.
// Images and other binary files are returned as a data URI; text files are
// returned as plain text so the widget can inline them into the prompt.
type droppedFile struct {
	Path    string `json:"path,omitempty"`
	Name    string `json:"name"`
	MIME    string `json:"mime"`
	Size    int64  `json:"size"`
	DataURI string `json:"data_uri,omitempty"`
	Text    string `json:"text,omitempty"`
	IsImage bool   `json:"is_image"`
}

// maxDroppedBytes caps how much of a dropped file we read into memory before
// handing it to the frontend. The prompt is inlined, so keep it sane.
const maxDroppedBytes = 8 * 1024 * 1024 // 8 MB

// ReadDroppedFile reads a file at an absolute path (as delivered by the Wails
// native OnFileDrop event) and returns its contents in a form the webview can
// attach. The webview cannot read file:// URLs itself, so this Go-side reader
// is the only way to ingest native drops on WebKitGTK.
func (a *App) ReadDroppedFile(path string) (*droppedFile, error) {
	cleaned := filepath.Clean(path)
	if cleaned == "" || !filepath.IsAbs(cleaned) {
		return nil, errors.New("path must be absolute")
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("directory drop not supported")
	}
	if info.Size() > maxDroppedBytes {
		return nil, errors.New("file too large (max 8 MB)")
	}

	f, err := os.Open(cleaned)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	limited := io.LimitReader(f, maxDroppedBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxDroppedBytes {
		return nil, errors.New("file too large (max 8 MB)")
	}

	name := filepath.Base(cleaned)
	mimeType := mime.TypeByExtension(filepath.Ext(name))
	if mimeType == "" {
		mimeType = httpDetectMime(raw, name)
	}
	isImage := strings.HasPrefix(mimeType, "image/")

	out := &droppedFile{Path: cleaned, Name: name, MIME: mimeType, Size: int64(len(raw)), IsImage: isImage}
	if isTextual(mimeType, name, raw) && !isImage {
		out.Text = string(raw)
		return out, nil
	}
	out.DataURI = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(raw)
	return out, nil
}

// OpenAttachment reveals a local attachment in the platform file manager.
func (a *App) OpenAttachment(path string) error {
	cleaned := filepath.Clean(path)
	if cleaned == "" || !filepath.IsAbs(cleaned) {
		return errors.New("attachment path must be absolute")
	}
	if _, err := os.Stat(cleaned); err != nil {
		return err
	}
	target := filepath.Dir(cleaned)
	var command *exec.Cmd
	switch {
	case commandExists("nautilus"):
		command = exec.Command("nautilus", "--select", cleaned)
	case commandExists("gio"):
		command = exec.Command("gio", "open", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	return command.Start()
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// isTextual decides whether a file should be inlined as text rather than as a
// base64 data URI. We trust the MIME type when present and fall back to a
// quick binary-content sniff for extensionless/text-like files.
func isTextual(mimeType, name string, raw []byte) bool {
	if strings.HasPrefix(mimeType, "text/") ||
		mimeType == "application/json" ||
		mimeType == "application/xml" ||
		mimeType == "application/javascript" ||
		mimeType == "application/x-sh" ||
		mimeType == "application/x-yaml" {
		return true
	}
	// No text MIME: treat as text only if it has a text-ish extension and no
	// NUL bytes (binary marker) in the first chunk.
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".txt", ".md", ".markdown", ".log", ".csv", ".tsv", ".json", ".yaml", ".yml", ".xml", ".html", ".htm", ".css", ".js", ".ts", ".go", ".py", ".rs", ".sh", ".toml", ".ini", ".env":
		for _, b := range raw {
			if b == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// httpDetectMime is a tiny fallback for files without a known extension.
func httpDetectMime(raw []byte, name string) string {
	if len(raw) >= 4 {
		switch {
		case raw[0] == 0xFF && raw[1] == 0xD8 && raw[2] == 0xFF:
			return "image/jpeg"
		case raw[0] == 0x89 && raw[1] == 0x50 && raw[2] == 0x4E && raw[3] == 0x47:
			return "image/png"
		case raw[0] == 0x47 && raw[1] == 0x49 && raw[2] == 0x46:
			return "image/gif"
		case raw[0] == 0x42 && raw[1] == 0x4D:
			return "image/bmp"
		case raw[0] == 0x52 && raw[1] == 0x49 && raw[2] == 0x46 && raw[3] == 0x46 && len(raw) >= 12 && raw[8] == 0x57 && raw[9] == 0x45 && raw[10] == 0x42 && raw[11] == 0x50:
			return "image/webp"
		}
	}
	return "application/octet-stream"
}
