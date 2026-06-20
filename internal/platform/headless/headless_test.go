package headless

import (
	"context"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/platform"
)

func TestHeadlessHasNoCapabilities(t *testing.T) {
	d := New()
	if len(d.Capabilities()) != 0 {
		t.Fatalf("headless should expose no capabilities, got %v", d.Capabilities())
	}
	if platform.Has(d.Capabilities(), platform.CapNotify) {
		t.Fatalf("headless should not advertise notify")
	}
	if d.Info().AdapterID != "headless-v1" {
		t.Fatalf("unexpected adapter id %q", d.Info().AdapterID)
	}
}

func TestHeadlessNotifyIsNoOp(t *testing.T) {
	d := New()
	if err := d.NotifySend(context.Background(), platform.Notification{Title: "x", Body: "y"}); err != nil {
		t.Fatalf("headless notify should be a no-op, got err %v", err)
	}
}

func TestHeadlessNotifyWatchClosedChannel(t *testing.T) {
	d := New()
	ch, err := d.NotifyWatch(context.Background())
	if err != nil {
		t.Fatalf("watch err: %v", err)
	}
	// Closed channel: range exits immediately.
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Fatalf("expected no events from headless watch, got %d", count)
	}
}

func TestHeadlessDNDOff(t *testing.T) {
	d := New()
	on, err := d.DNDEnabled(context.Background())
	if err != nil || on {
		t.Fatalf("headless DND should be (false,nil), got (%v,%v)", on, err)
	}
}

// Compile-time check: headless implements the Desktop interface.
var _ platform.Desktop = (*Desktop)(nil)
