package bus

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	publishMu sync.Mutex
	mu        sync.RWMutex
	subs      map[*subscriber]struct{}

	seq int64

	walCh   chan Event
	walDone chan struct{}
	walPath string
	walMu   sync.RWMutex
	closed  bool
}

const (
	defaultWALMaxBytes  int64 = 16 << 20
	defaultWALKeepFiles       = 3
)

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
	// Seed across the primary and rotated siblings so rotation never resets seq.
	if last, err := lastSeqAll(walPath); err == nil {
		b.seq = last
	}
	b.walPath = walPath
	b.walCh = make(chan Event, 1024)
	b.walDone = make(chan struct{})
	go b.runWAL(b.walCh)
	return b, nil
}

func (b *Bus) runWAL(events <-chan Event) {
	defer close(b.walDone)
	f, err := openWAL(b.walPath)
	if err != nil {
		// Drain to avoid blocking publishers if the file can't be opened.
		for range events {
		}
		return
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	var size int64
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	for ev := range events {
		if line, err := json.Marshal(ev); err == nil {
			line = append(line, '\n')
			if size > 0 && size+int64(len(line)) > defaultWALMaxBytes {
				_ = w.Flush()
				_ = f.Close()
				if rotateErr := rotateWAL(b.walPath, defaultWALKeepFiles); rotateErr == nil {
					f, err = openWAL(b.walPath)
					if err != nil {
						for range events {
						}
						return
					}
					w = bufio.NewWriter(f)
					size = 0
				} else {
					f, err = openWAL(b.walPath)
					if err != nil {
						for range events {
						}
						return
					}
					w = bufio.NewWriter(f)
				}
			}
			_, _ = w.Write(line)
			_ = w.Flush()
			size += int64(len(line))
		}
	}
}

func openWAL(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func rotateWAL(path string, keep int) error {
	_ = os.Remove(path + "." + fmt.Sprint(keep))
	for n := keep - 1; n >= 1; n-- {
		src := path + "." + fmt.Sprint(n)
		dst := path + "." + fmt.Sprint(n+1)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		}
	}
	return os.Rename(path, path+".1")
}

// Close stops the WAL goroutine (if any). Safe to call once; the bus must not
// be published to afterwards.
func (b *Bus) Close() {
	b.walMu.Lock()
	if b.closed {
		b.walMu.Unlock()
		return
	}
	b.closed = true
	ch, done := b.walCh, b.walDone
	b.walCh = nil
	if ch != nil {
		close(ch)
	}
	b.walMu.Unlock()
	if done != nil {
		<-done
	}
}

// Publish delivers data on topic to every subscriber whose patterns match, and
// appends it to the WAL when enabled. Non-blocking: a full subscriber buffer
// drops the event.
func (b *Bus) Publish(topic string, data bridge.StreamEvent) {
	// Seq is the delivery order contract, not just a unique id. Serialize the
	// assignment and every downstream enqueue so concurrent publishers cannot
	// deliver or persist seq N+1 before seq N.
	b.publishMu.Lock()
	defer b.publishMu.Unlock()
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

	b.walMu.RLock()
	if b.walCh != nil && !b.closed {
		// A configured WAL is a durability contract: apply backpressure instead
		// of silently dropping the only replay copy.
		b.walCh <- ev
	}
	b.walMu.RUnlock()
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
	for _, path := range walPathsOldestFirst(b.walPath) {
		if err := replayFile(path, since, fn); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func replayFile(path string, since int64, fn func(Event)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Seq > since {
			fn(ev)
		}
	}
	return sc.Err()
}

func walPathsOldestFirst(path string) []string {
	paths := make([]string, 0, defaultWALKeepFiles+1)
	for n := defaultWALKeepFiles; n >= 1; n-- {
		paths = append(paths, path+"."+fmt.Sprint(n))
	}
	return append(paths, path)
}

func lastSeqAll(path string) (int64, error) {
	var max int64
	for _, candidate := range walPathsOldestFirst(path) {
		seq, err := lastSeq(candidate)
		if err != nil && !os.IsNotExist(err) {
			return max, err
		}
		if seq > max {
			max = seq
		}
	}
	return max, nil
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
