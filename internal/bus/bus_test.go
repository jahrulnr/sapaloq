package bus

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

func TestMatchTopic(t *testing.T) {
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"sapaloq.v1.subagent.spawned", "sapaloq.v1.subagent.spawned", true},
		{"sapaloq.v1.subagent.*", "sapaloq.v1.subagent.spawned", true},
		{"sapaloq.v1.subagent.*", "sapaloq.v1.subagent.spawned.extra", false},
		{"sapaloq.v1.**", "sapaloq.v1.subagent.spawned", true},
		{"sapaloq.v1.**", "sapaloq.v1", false}, // ** requires >=1 remaining segment
		{"**", "anything.at.all", true},
		{"sapaloq.*.subagent", "sapaloq.v1.subagent", true},
		{"sapaloq.*.subagent", "sapaloq.subagent", false},
		{"sapaloq.v1.subagent", "sapaloq.v1.task", false},
	}
	for _, c := range cases {
		if got := matchTopic(c.pattern, c.topic); got != c.want {
			t.Errorf("matchTopic(%q,%q)=%v want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

func TestSubscribeTopicsFiltering(t *testing.T) {
	b := New()
	subA, cancelA := b.SubscribeTopics([]string{"sapaloq.v1.subagent.*"}, 8)
	defer cancelA()
	subB, cancelB := b.SubscribeTopics([]string{"sapaloq.v1.task.*"}, 8)
	defer cancelB()

	b.Publish("sapaloq.v1.subagent.spawned", bridge.StreamEvent{Kind: bridge.EventDone})
	b.Publish("sapaloq.v1.task.created", bridge.StreamEvent{Kind: bridge.EventDone})

	gotA := drain(subA, 1)
	gotB := drain(subB, 1)
	if len(gotA) != 1 || gotA[0].Topic != "sapaloq.v1.subagent.spawned" {
		t.Fatalf("sub A got %+v", gotA)
	}
	if len(gotB) != 1 || gotB[0].Topic != "sapaloq.v1.task.created" {
		t.Fatalf("sub B got %+v", gotB)
	}
}

func TestSubscribeReceiveAll(t *testing.T) {
	b := New()
	sub, cancel := b.Subscribe(8) // backward-compatible: receives everything
	defer cancel()
	b.Publish("a.b.c", bridge.StreamEvent{Kind: bridge.EventDone})
	b.Publish("x.y.z", bridge.StreamEvent{Kind: bridge.EventDone})
	got := drain(sub, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
}

func TestWALAppendAndReplay(t *testing.T) {
	wal := filepath.Join(t.TempDir(), "bus.jsonl")
	b, err := NewWithWAL(wal)
	if err != nil {
		t.Fatalf("new wal: %v", err)
	}
	for i := 0; i < 5; i++ {
		b.Publish("sapaloq.v1.task.created", bridge.StreamEvent{Kind: bridge.EventDone})
	}
	b.Close() // flush + close WAL goroutine

	// Fresh bus replays the persisted events in order.
	b2, err := NewWithWAL(wal)
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	defer b2.Close()
	var seen []Event
	if err := b2.Replay(0, func(ev Event) { seen = append(seen, ev) }); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 replayed events, got %d", len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if seen[i].Seq <= seen[i-1].Seq {
			t.Fatalf("seq not monotonic: %d then %d", seen[i-1].Seq, seen[i].Seq)
		}
	}

	// Replay(since) skips already-seen events.
	var tail []Event
	_ = b2.Replay(seen[2].Seq, func(ev Event) { tail = append(tail, ev) })
	if len(tail) != 2 {
		t.Fatalf("expected 2 events after seq %d, got %d", seen[2].Seq, len(tail))
	}
}

func TestWALSeqMonotonicAcrossBoots(t *testing.T) {
	wal := filepath.Join(t.TempDir(), "bus.jsonl")
	b, _ := NewWithWAL(wal)
	b.Publish("a.b.c", bridge.StreamEvent{Kind: bridge.EventDone})
	b.Close()

	b2, _ := NewWithWAL(wal)
	defer b2.Close()
	b2.Publish("a.b.c", bridge.StreamEvent{Kind: bridge.EventDone})
	b2.Close()

	var seqs []int64
	b3, _ := NewWithWAL(wal)
	defer b3.Close()
	_ = b3.Replay(0, func(ev Event) { seqs = append(seqs, ev.Seq) })
	if len(seqs) != 2 || seqs[0] != 1 || seqs[1] != 2 {
		t.Fatalf("expected seqs [1 2] across boots, got %v", seqs)
	}
}

func TestPublishNonBlocking(t *testing.T) {
	b := New()
	// Buffer of 1, never drained → second publish must not block.
	_, cancel := b.SubscribeTopics([]string{"**"}, 1)
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			b.Publish("a.b.c", bridge.StreamEvent{Kind: bridge.EventDone})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full subscriber buffer")
	}
}

func drain(ch <-chan Event, n int) []Event {
	var out []Event
	timeout := time.After(500 * time.Millisecond)
	for len(out) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			return out
		}
	}
	return out
}
