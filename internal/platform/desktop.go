// Package platform abstracts the host desktop environment behind a small,
// OS-agnostic interface so the orchestrator can send notifications (and, later,
// query/focus windows, take screenshots, etc.) without importing any D-Bus or
// GNOME types. Concrete backends live in sub-packages (headless, freedesktop);
// the core only ever depends on this package.
//
// Design: docs/PLATFORM.md.
package platform

import (
	"context"
	"time"
)

// Capability is a discrete desktop feature an adapter may support. Tools check
// the active adapter's capability set before acting so a missing feature
// degrades gracefully instead of erroring deep in a backend.
type Capability string

const (
	CapNotify      Capability = "notify"
	CapNotifyWatch Capability = "notify.watch"
	CapDND         Capability = "dnd"
	CapScreenshot  Capability = "screenshot"
	CapWindowList  Capability = "window.list"
	CapWindowFocus Capability = "window.focus"
	CapClipboard   Capability = "clipboard"
	CapTray        Capability = "tray"
)

// Info describes the detected environment + the adapter chosen for it.
type Info struct {
	OS           string       `json:"os"`
	DE           string       `json:"de"`
	Session      string       `json:"session"`
	AdapterID    string       `json:"adapter_id"`
	Capabilities []Capability `json:"capabilities"`
}

// Notification is an outgoing desktop notification request.
type Notification struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Icon    string `json:"icon,omitempty"`
	Urgency string `json:"urgency,omitempty"` // low | normal | critical
}

// NotificationEvent is an incoming notification observed on the desktop bus
// (best-effort; only adapters with CapNotifyWatch emit these).
type NotificationEvent struct {
	AppName string    `json:"app_name"`
	Summary string    `json:"summary"`
	Body    string    `json:"body"`
	At      time.Time `json:"at"`
}

// Desktop is the minimal host-desktop abstraction the orchestrator consumes.
// Phase 1 covers notifications + DND; window/screenshot/clipboard are declared
// as capabilities but not yet part of the interface (added in later phases).
type Desktop interface {
	// Info returns the detected environment and chosen adapter.
	Info() Info
	// Capabilities returns the features this adapter actually supports.
	Capabilities() []Capability
	// NotifySend posts a desktop notification. Returns an error when the
	// adapter lacks CapNotify or the backend call fails.
	NotifySend(ctx context.Context, n Notification) error
	// NotifyWatch returns a channel of incoming notifications. Adapters without
	// CapNotifyWatch return a closed channel (range exits immediately).
	NotifyWatch(ctx context.Context) (<-chan NotificationEvent, error)
	// DNDEnabled reports whether Do-Not-Disturb is active. Adapters without
	// CapDND return (false, nil).
	DNDEnabled(ctx context.Context) (bool, error)
}
