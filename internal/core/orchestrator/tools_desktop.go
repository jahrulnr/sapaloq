package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/platform"
	"github.com/jahrulnr/sapaloq/internal/platform/headless"
)

// desktopAdapter returns the active desktop adapter, defaulting to a headless
// one when none was wired (e.g. zero-value Orchestrator in a test).
func (o *Orchestrator) desktopAdapter() platform.Desktop {
	if o != nil && o.desktop != nil {
		return o.desktop
	}
	return headless.New()
}

// Desktop exposes the active desktop adapter (used by the core to start the
// notify-watch → bus bridge). Never nil.
func (o *Orchestrator) Desktop() platform.Desktop { return o.desktopAdapter() }

// toolDesktopNotify sends a desktop notification, checking the adapter's
// capability first so a headless/unsupported host degrades to a clear message
// instead of erroring.
func (o *Orchestrator) toolDesktopNotify(ctx context.Context, args toolArgs) string {
	d := o.desktopAdapter()
	if !platform.Has(d.Capabilities(), platform.CapNotify) {
		return fmt.Sprintf("Notifications are not supported on this adapter (%s).", d.Info().AdapterID)
	}
	title := strings.TrimSpace(args.Title)
	body := strings.TrimSpace(args.Body)
	if title == "" && body == "" {
		return "Error: title or body is required."
	}
	if title == "" {
		title = "SapaLOQ"
	}
	urgency := strings.ToLower(strings.TrimSpace(args.Urgency))
	switch urgency {
	case "", "low", "normal", "critical":
	default:
		urgency = "normal"
	}
	if err := d.NotifySend(ctx, platform.Notification{Title: title, Body: body, Urgency: urgency}); err != nil {
		return "Error sending notification: " + err.Error()
	}
	return "Notification sent."
}

// toolDesktopDNDStatus reports Do-Not-Disturb state when the adapter supports
// it, otherwise a clear unsupported message.
func (o *Orchestrator) toolDesktopDNDStatus(ctx context.Context) string {
	d := o.desktopAdapter()
	if !platform.Has(d.Capabilities(), platform.CapDND) {
		return fmt.Sprintf("DND status is not available on this adapter (%s).", d.Info().AdapterID)
	}
	on, err := d.DNDEnabled(ctx)
	if err != nil {
		return "Error reading DND status: " + err.Error()
	}
	if on {
		return "Do-Not-Disturb is ON."
	}
	return "Do-Not-Disturb is OFF."
}

// NotificationStreamEvent maps an incoming desktop notification into the bus
// stream payload (reusing the generic status event shape: status="notification",
// delta carries a human-readable line). Exported for the core's watch bridge.
func NotificationStreamEvent(ev platform.NotificationEvent) bridge.StreamEvent {
	line := ev.Summary
	if ev.AppName != "" {
		line = ev.AppName + ": " + ev.Summary
	}
	if ev.Body != "" {
		line += " - " + ev.Body
	}
	se := bridge.NewEvent(bridge.EventStatus)
	se.Status = "notification"
	se.Delta = line
	if !ev.At.IsZero() {
		se.At = ev.At
	}
	return se
}
