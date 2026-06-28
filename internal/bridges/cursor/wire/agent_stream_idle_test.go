package wire

import (
	"context"
	"testing"
	"time"
)

func TestStreamIdleWatchResetsOnActivity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watch := NewStreamIdleWatch(cancel, 80*time.Millisecond)
	if watch == nil {
		t.Fatal("expected idle watch")
	}
	defer watch.Stop()

	time.Sleep(50 * time.Millisecond)
	watch.Reset()
	time.Sleep(50 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatalf("idle fired too early: %v", ctx.Err())
	}

	time.Sleep(90 * time.Millisecond)
	if ctx.Err() == nil {
		t.Fatal("expected idle cancel after silence")
	}
	if !watch.TimedOut() {
		t.Fatal("TimedOut = false, want true")
	}
}

func TestStreamIdleWatchPauseDuringWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watch := NewStreamIdleWatch(cancel, 40*time.Millisecond)
	defer watch.Stop()

	watch.Pause()
	time.Sleep(90 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatalf("idle fired during pause: %v", ctx.Err())
	}
	watch.Resume()
	time.Sleep(25 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatalf("idle fired before refill window: %v", ctx.Err())
	}
}
