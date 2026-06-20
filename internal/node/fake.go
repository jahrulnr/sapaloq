package node

import (
	"context"
	"sync"
)

// FakeTransport is an in-memory Transport for tests. It replays a scripted
// sequence of Progress updates and records the envelopes it received, so the
// orchestrator's remote-routing wiring can be unit-tested without a network.
type FakeTransport struct {
	// Script is the sequence of progress updates emitted on Spawn.
	Script []Progress
	// SpawnErr, when set, is returned by Spawn (simulates a connect failure).
	SpawnErr error

	mu        sync.Mutex
	Spawned   []SpawnEnvelope
	Controls  []string
	ClosedCnt int
}

// Spawn emits the scripted progress (or returns SpawnErr).
func (f *FakeTransport) Spawn(ctx context.Context, env SpawnEnvelope) (<-chan Progress, error) {
	if f.SpawnErr != nil {
		return nil, f.SpawnErr
	}
	f.mu.Lock()
	f.Spawned = append(f.Spawned, env)
	script := append([]Progress(nil), f.Script...)
	f.mu.Unlock()

	ch := make(chan Progress, len(script)+1)
	go func() {
		defer close(ch)
		for _, p := range script {
			select {
			case <-ctx.Done():
				return
			case ch <- p:
			}
		}
	}()
	return ch, nil
}

// Control records the action.
func (f *FakeTransport) Control(ctx context.Context, subAgentID, action string) error {
	f.mu.Lock()
	f.Controls = append(f.Controls, subAgentID+":"+action)
	f.mu.Unlock()
	return nil
}

// Close records the close call.
func (f *FakeTransport) Close() error {
	f.mu.Lock()
	f.ClosedCnt++
	f.mu.Unlock()
	return nil
}

// LastEnvelope returns the most recent spawn envelope (or zero value).
func (f *FakeTransport) LastEnvelope() (SpawnEnvelope, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Spawned) == 0 {
		return SpawnEnvelope{}, false
	}
	return f.Spawned[len(f.Spawned)-1], true
}

var _ Transport = (*FakeTransport)(nil)
