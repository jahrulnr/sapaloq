package bus

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

// Event is one published message. Topic is a dot-delimited routing key
// (e.g. "sapaloq.v1.subagent.spawned"); Data is the stream payload.
type Event struct {
	Seq   int64              `json:"seq"`
	At    string             `json:"at"`
	Topic string             `json:"topic"`
	Data  bridge.StreamEvent `json:"data"`
}

type subscriber struct {
	ch       chan Event
	patterns []string
}

// Bus is an in-process pub/sub with optional topic-pattern routing and a
// JSON-lines write-ahead log for replay across restarts. Publish never blocks:
// full subscriber buffers drop the event (same behavior as before), and WAL
// appends are handed to a serialized background goroutine.
type Bus struct {
	mu   sync.RWMutex
	subs map[*subscriber]struct{}

	seq int64

	walCh   chan Event
	walDone chan struct{}
	walPath string
}

// New returns a bus with no WAL (in-memory only).
func New() *Bus {
	return &Bus{subs: map[*subscriber]struct{}{}}
}

// NewWithWAL returns a bus that appends every published event to a JSON-lines
// WAL at walPath. The WAL is written by a dedicated goroutine so Publish stays
// non-blocking. An empty walPath behaves like New().
func NewWithWAL(walPath string) (*Bus, error) {
	b := New()
	if strings.TrimSpace(walPath) == "" {
		return b, nil
	}
	if err := os.MkdirAll(filepath.Dir(walPath), 0o755); err != nil {
		return nil, err
	}
	// Touch/seed seq from the existing WAL tail so seq is monotonic across boots.
	if last, err := lastSeq(walPath); err == nil {
		b.seq = last
	}
	b.walPath = walPath
	b.walCh = make(chan Event, 1024)
	b.walDone = make(chan struct{})
	go b.runWAL()
	return b, nil
}

func (b *Bus) runWAL() {
	defer close(b.walDone)
	f, err := os.OpenFile(b.walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		// Drain to avoid blocking publishers if the file can't be opened.
		for range b.walCh {
		}
		return
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for ev := range b.walCh {
		if line, err := json.Marshal(ev); err == nil {
			_, _ = w.Write(line)
			_ = w.WriteByte('\n')
			_ = w.Flush()
		}
	}
}

// Close stops the WAL goroutine (if any). Safe to call once; the bus must not
// be published to afterwards.
func (b *Bus) Close() {
	if b.walCh != nil {
		close(b.walCh)
		<-b.walDone
		b.walCh = nil
	}
}

// Publish delivers data on topic to every subscriber whose patterns match, and
// appends it to the WAL when enabled. Non-blocking: a full subscriber buffer
// drops the event.
func (b *Bus) Publish(topic string, data bridge.StreamEvent) {
	ev := Event{
		Seq:   atomic.AddInt64(&b.seq, 1),
		At:    time.Now().UTC().Format(time.RFC3339Nano),
		Topic: topic,
		Data:  data,
	}
	b.mu.RLock()
	for sub := range b.subs {
		if !matchAny(sub.patterns, topic) {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
		}
	}
	b.mu.RUnlock()

	if b.walCh != nil {
		select {
		case b.walCh <- ev:
		default: // WAL backpressure: drop rather than block publishers.
		}
	}
}

// Subscribe registers a receive-all subscriber (backward-compatible default).
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	return b.SubscribeTopics([]string{"**"}, buffer)
}

// SubscribeTopics registers a subscriber that only receives events whose topic
// matches one of the patterns. Patterns use "." segments where "*" matches a
// single segment and "**" matches the remainder (must be the final segment).
func (b *Bus) SubscribeTopics(patterns []string, buffer int) (<-chan Event, func()) {
	if len(patterns) == 0 {
		patterns = []string{"**"}
	}
	sub := &subscriber{ch: make(chan Event, buffer), patterns: patterns}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subs[sub]; ok {
			delete(b.subs, sub)
			close(sub.ch)
		}
		b.mu.Unlock()
	}
	return sub.ch, cancel
}

// Replay reads the WAL and invokes fn for every event with Seq > since, in
// stored order. It is a no-op when no WAL is configured or the file is absent.
func (b *Bus) Replay(since int64, fn func(Event)) error {
	if b.walPath == "" || fn == nil {
		return nil
	}
	f, err := os.Open(b.walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue // skip corrupt lines rather than aborting replay
		}
		if ev.Seq > since {
			fn(ev)
		}
	}
	return sc.Err()
}

// lastSeq scans the WAL and returns the highest seq seen (0 when empty/absent).
func lastSeq(walPath string) (int64, error) {
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()
	var max int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Seq > max {
			max = ev.Seq
		}
	}
	return max, sc.Err()
}

// matchAny reports whether topic matches any of the patterns.
func matchAny(patterns []string, topic string) bool {
	for _, p := range patterns {
		if matchTopic(p, topic) {
			return true
		}
	}
	return false
}

// matchTopic matches a dot-delimited topic against a pattern. "*" matches
// exactly one segment; a trailing "**" matches one or more remaining segments
// (and is only meaningful as the final pattern segment).
func matchTopic(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	pp := strings.Split(pattern, ".")
	tt := strings.Split(topic, ".")
	for i, seg := range pp {
		if seg == "**" {
			// Matches the rest, provided there is at least one remaining segment.
			return i < len(tt)
		}
		if i >= len(tt) {
			return false
		}
		if seg == "*" || seg == tt[i] {
			continue
		}
		return false
	}
	// Pattern fully consumed: match only if topic has no extra segments.
	return len(pp) == len(tt)
}
