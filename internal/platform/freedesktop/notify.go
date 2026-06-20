// Package freedesktop implements the platform.Desktop interface on top of the
// freedesktop.org D-Bus Notifications spec (org.freedesktop.Notifications). It
// works on most Linux desktops (GNOME, KDE, etc.) that expose a session bus.
//
// The constructor probes the session bus; if unavailable it returns an error so
// platform.Detect falls back to headless and the core never fails to start. The
// GNOME adapter id reuses this implementation (notifications are the same spec).
//
// NotifyWatch eavesdrops on the Notify method via a D-Bus monitor match. This is
// best-effort and environment-sensitive (some buses restrict eavesdropping); a
// failure simply yields no incoming events while NotifySend keeps working.
package freedesktop

import (
	"context"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/jahrulnr/sapaloq/internal/platform"
)

const (
	notifyService = "org.freedesktop.Notifications"
	notifyPath    = "/org/freedesktop/Notifications"
	notifyIface   = "org.freedesktop.Notifications"
)

// Desktop is the freedesktop D-Bus adapter.
type Desktop struct {
	conn      *dbus.Conn
	adapterID string
	de        string
}

// Option tunes adapter construction.
type Option func(*Desktop)

// WithAdapterID overrides the reported adapter id (e.g. "gnome-v1").
func WithAdapterID(id string) Option {
	return func(d *Desktop) { d.adapterID = id }
}

// WithDE sets the reported desktop-environment string.
func WithDE(de string) Option {
	return func(d *Desktop) { d.de = de }
}

// New connects to the session bus. Returns an error when no session bus is
// reachable so the caller can fall back to headless.
func New(opts ...Option) (*Desktop, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("freedesktop: session bus unavailable: %w", err)
	}
	d := &Desktop{conn: conn, adapterID: "freedesktop-v1", de: "freedesktop"}
	for _, o := range opts {
		o(d)
	}
	return d, nil
}

// Factory adapts New to platform.Factory (returns the interface type).
func Factory(opts ...Option) platform.Factory {
	return func() (platform.Desktop, error) {
		d, err := New(opts...)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
}

func (d *Desktop) Info() platform.Info {
	return platform.Info{
		OS:           "linux",
		DE:           d.de,
		Session:      "dbus",
		AdapterID:    d.adapterID,
		Capabilities: d.Capabilities(),
	}
}

func (d *Desktop) Capabilities() []platform.Capability {
	return []platform.Capability{platform.CapNotify, platform.CapNotifyWatch}
}

// NotifySend posts a notification via org.freedesktop.Notifications.Notify.
func (d *Desktop) NotifySend(ctx context.Context, n platform.Notification) error {
	obj := d.conn.Object(notifyService, dbus.ObjectPath(notifyPath))
	hints := map[string]dbus.Variant{}
	switch n.Urgency {
	case "low":
		hints["urgency"] = dbus.MakeVariant(byte(0))
	case "critical":
		hints["urgency"] = dbus.MakeVariant(byte(2))
	default:
		hints["urgency"] = dbus.MakeVariant(byte(1))
	}
	appName := "SapaLOQ"
	icon := n.Icon
	timeout := int32(-1) // server default
	call := obj.CallWithContext(ctx, notifyIface+".Notify", 0,
		appName,    // app_name
		uint32(0),  // replaces_id
		icon,       // app_icon
		n.Title,    // summary
		n.Body,     // body
		[]string{}, // actions
		hints,      // hints
		timeout,    // expire_timeout
	)
	if call.Err != nil {
		return fmt.Errorf("freedesktop: notify failed: %w", call.Err)
	}
	return nil
}

// NotifyWatch eavesdrops on outgoing Notify calls. Best-effort: if adding the
// monitor match fails (restricted bus), it returns a closed channel.
func (d *Desktop) NotifyWatch(ctx context.Context) (<-chan platform.NotificationEvent, error) {
	out := make(chan platform.NotificationEvent, 16)
	match := []dbus.MatchOption{
		dbus.WithMatchObjectPath(notifyPath),
		dbus.WithMatchInterface(notifyIface),
		dbus.WithMatchMember("Notify"),
	}
	// Eavesdrop requires monitoring; AddMatchSignal won't catch method calls on
	// a normal connection. Try the monitor match; on failure, close and return.
	if err := d.conn.AddMatchSignalContext(ctx, match...); err != nil {
		close(out)
		return out, nil
	}
	sig := make(chan *dbus.Signal, 16)
	d.conn.Signal(sig)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-sig:
				if !ok {
					return
				}
				ev, ok := signalToNotification(s)
				if !ok {
					continue
				}
				select {
				case out <- ev:
				default:
				}
			}
		}
	}()
	return out, nil
}

// DNDEnabled is not exposed by the freedesktop notification spec; report off.
func (d *Desktop) DNDEnabled(ctx context.Context) (bool, error) { return false, nil }

// signalToNotification extracts a NotificationEvent from a Notify signal body
// (app_name, replaces_id, icon, summary, body, ...). Returns ok=false when the
// signal is unrelated or malformed.
func signalToNotification(s *dbus.Signal) (platform.NotificationEvent, bool) {
	if s == nil || len(s.Body) < 5 {
		return platform.NotificationEvent{}, false
	}
	appName, _ := s.Body[0].(string)
	summary, _ := s.Body[3].(string)
	body, _ := s.Body[4].(string)
	if summary == "" && body == "" {
		return platform.NotificationEvent{}, false
	}
	return platform.NotificationEvent{
		AppName: appName,
		Summary: summary,
		Body:    body,
		At:      time.Now().UTC(),
	}, true
}

// Compile-time interface check.
var _ platform.Desktop = (*Desktop)(nil)
