// Package node defines the transport abstraction for routing sub-agent spawns
// to remote execution nodes (see docs/NODES.md). Local nodes run in-process and
// do not use a Transport; remote nodes receive only a bounded context packet -
// NEVER the memory bus or companion.db.
//
// The interface is intentionally minimal so it is fully unit-testable via the
// in-memory fake (fake.go) without any network, while ws.go provides the real
// WebSocket client behind a connect probe.
package node

import "context"

// SpawnEnvelope is the bounded payload sent to a remote node. It carries only
// what the remote needs to execute - no memory bus, no full chat history.
type SpawnEnvelope struct {
	SubAgentID   string `json:"sub_agent_id"`
	Role         string `json:"role"`
	Task         string `json:"task"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	// ContextPacket is a small, explicit bag of context (taskId, mode, a user
	// snippet, a few relevant facts, a config snapshot). It must never include
	// the memory bus or raw companion.db rows.
	ContextPacket map[string]any `json:"context_packet,omitempty"`
	// NoMemoryBus is always true for remote nodes; it documents + enforces the
	// invariant at the envelope boundary.
	NoMemoryBus bool `json:"no_memory_bus"`
}

// Progress is one streamed update from a remote node's execution.
type Progress struct {
	Kind  string `json:"kind"` // progress | thinking | result | error
	Text  string `json:"text"`
	Done  bool   `json:"done"`
	Error string `json:"error,omitempty"`
}

// Transport spawns a sub-agent on a remote node and streams its progress.
type Transport interface {
	// Spawn starts the remote sub-agent and returns a channel of progress
	// updates. The channel is closed when the remote task terminates. A
	// connect/handshake failure returns an error so the orchestrator can fall
	// back to local-default (when allowed).
	Spawn(ctx context.Context, env SpawnEnvelope) (<-chan Progress, error)
	// Control sends a lifecycle action ("stop", "pause") for a running task.
	Control(ctx context.Context, subAgentID, action string) error
	// Close releases transport resources.
	Close() error
}

// EnforceRemoteInvariants returns a copy of env with the remote-safety
// invariants applied: NoMemoryBus forced true and any memory-bus-ish context
// keys stripped. Callers MUST pass remote envelopes through this before Spawn.
func EnforceRemoteInvariants(env SpawnEnvelope) SpawnEnvelope {
	env.NoMemoryBus = true
	if env.ContextPacket != nil {
		for _, banned := range []string{"memory_bus", "companion_db", "bus", "facts_db"} {
			delete(env.ContextPacket, banned)
		}
	}
	return env
}
