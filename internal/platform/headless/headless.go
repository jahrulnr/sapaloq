// Package headless is the always-available fallback Desktop adapter. It exposes
// no real desktop capabilities: notifications are no-ops (best-effort logged via
// the returned info), the watch channel is closed immediately, and DND is off.
// This guarantees the core builds and runs on CI and non-Linux hosts.
package headless

import (
	"context"

	"github.com/jahrulnr/sapaloq/internal/platform"
)

// Desktop is the headless adapter.
type Desktop struct {
	info platform.Info
}

// New returns a headless adapter with no capabilities.
func New() *Desktop {
	return &Desktop{
		info: platform.Info{
			OS:           "unknown",
			DE:           "none",
			Session:      "headless",
			AdapterID:    "headless-v1",
			Capabilities: nil,
		},
	}
}

func (d *Desktop) Info() platform.Info                 { return d.info }
func (d *Desktop) Capabilities() []platform.Capability { return d.info.Capabilities }

// NotifySend is a no-op on headless. It returns nil so callers that already
// checked CapNotify (which headless lacks) never reach here, and direct callers
// degrade silently rather than error.
func (d *Desktop) NotifySend(ctx context.Context, n platform.Notification) error {
	return nil
}

// NotifyWatch returns a closed channel so `for range ch` exits immediately.
func (d *Desktop) NotifyWatch(ctx context.Context) (<-chan platform.NotificationEvent, error) {
	ch := make(chan platform.NotificationEvent)
	close(ch)
	return ch, nil
}

// DNDEnabled is always false on headless.
func (d *Desktop) DNDEnabled(ctx context.Context) (bool, error) { return false, nil }
