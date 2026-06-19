package bus

import (
	"sync"

	"github.com/jahrulnr/sapaloq/internal/bridge"
)

type Event struct {
	Topic string             `json:"topic"`
	Data  bridge.StreamEvent `json:"data"`
}

type Bus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func New() *Bus {
	return &Bus{subs: map[chan Event]struct{}{}}
}

func (b *Bus) Publish(topic string, data bridge.StreamEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- Event{Topic: topic, Data: data}:
		default:
		}
	}
}

func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	ch := make(chan Event, buffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}
