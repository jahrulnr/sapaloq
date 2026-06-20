package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/platform"
)

// fakeDesktop is a controllable Desktop for testing the capability gate.
type fakeDesktop struct {
	caps      []platform.Capability
	sent      []platform.Notification
	dnd       bool
	notifyErr error
}

func (f *fakeDesktop) Info() platform.Info {
	return platform.Info{AdapterID: "fake-v1", Capabilities: f.caps}
}
func (f *fakeDesktop) Capabilities() []platform.Capability { return f.caps }
func (f *fakeDesktop) NotifySend(ctx context.Context, n platform.Notification) error {
	if f.notifyErr != nil {
		return f.notifyErr
	}
	f.sent = append(f.sent, n)
	return nil
}
func (f *fakeDesktop) NotifyWatch(ctx context.Context) (<-chan platform.NotificationEvent, error) {
	ch := make(chan platform.NotificationEvent)
	close(ch)
	return ch, nil
}
func (f *fakeDesktop) DNDEnabled(ctx context.Context) (bool, error) { return f.dnd, nil }

func TestDesktopNotifyUnsupportedAdapter(t *testing.T) {
	o := &Orchestrator{desktop: &fakeDesktop{caps: nil}}
	got := o.toolDesktopNotify(context.Background(), toolArgs{Title: "Hi", Body: "there"})
	if !strings.Contains(got, "not supported") {
		t.Fatalf("expected unsupported message, got %q", got)
	}
}

func TestDesktopNotifySendsWhenCapable(t *testing.T) {
	fd := &fakeDesktop{caps: []platform.Capability{platform.CapNotify}}
	o := &Orchestrator{desktop: fd}
	got := o.toolDesktopNotify(context.Background(), toolArgs{Title: "Build", Body: "done", Urgency: "critical"})
	if !strings.Contains(got, "Notification sent") {
		t.Fatalf("expected success, got %q", got)
	}
	if len(fd.sent) != 1 || fd.sent[0].Title != "Build" || fd.sent[0].Urgency != "critical" {
		t.Fatalf("notification not recorded correctly: %+v", fd.sent)
	}
}

func TestDesktopNotifyRequiresTitleOrBody(t *testing.T) {
	o := &Orchestrator{desktop: &fakeDesktop{caps: []platform.Capability{platform.CapNotify}}}
	got := o.toolDesktopNotify(context.Background(), toolArgs{})
	if !strings.Contains(got, "required") {
		t.Fatalf("expected required-field error, got %q", got)
	}
}

func TestDesktopDNDStatus(t *testing.T) {
	o := &Orchestrator{desktop: &fakeDesktop{caps: []platform.Capability{platform.CapDND}, dnd: true}}
	if got := o.toolDesktopDNDStatus(context.Background()); !strings.Contains(got, "ON") {
		t.Fatalf("expected DND ON, got %q", got)
	}
	o2 := &Orchestrator{desktop: &fakeDesktop{caps: nil}}
	if got := o2.toolDesktopDNDStatus(context.Background()); !strings.Contains(got, "not available") {
		t.Fatalf("expected unsupported DND, got %q", got)
	}
}

func TestDesktopAdapterDefaultsHeadless(t *testing.T) {
	o := &Orchestrator{} // no desktop wired
	if o.desktopAdapter() == nil {
		t.Fatalf("desktopAdapter should never be nil")
	}
	if platform.Has(o.desktopAdapter().Capabilities(), platform.CapNotify) {
		t.Fatalf("default headless should not advertise notify")
	}
}

func TestNotificationStreamEvent(t *testing.T) {
	se := NotificationStreamEvent(platform.NotificationEvent{
		AppName: "Slack", Summary: "New message", Body: "hi", At: time.Now(),
	})
	if se.Status != "notification" {
		t.Fatalf("status should be notification, got %q", se.Status)
	}
	if !strings.Contains(se.Delta, "Slack") || !strings.Contains(se.Delta, "New message") {
		t.Fatalf("delta missing fields: %q", se.Delta)
	}
}
