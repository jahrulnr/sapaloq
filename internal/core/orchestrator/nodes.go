package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/jahrulnr/sapaloq/internal/config"
	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

const localDefaultNode = "local-default"

// bootstrapLocalDefaultNode ensures a "local-default" node row exists so spawns
// always have a routable in-proc target. Idempotent: a second call does not
// duplicate. Best-effort - errors are non-fatal (spawn falls back to local).
func (o *Orchestrator) bootstrapLocalDefaultNode(ctx context.Context) {
	if o == nil || o.chat == nil {
		return
	}
	if _, ok, err := o.chat.GetNode(ctx, localDefaultNode); err == nil && ok {
		return
	}
	specPath := config.ExpandPath("~/SapaLOQ/nodes/local-default.md")
	_ = o.chat.UpsertNode(ctx, chatstore.Node{
		Name:         localDefaultNode,
		Role:         "*",
		Wrapper:      "local",
		Communicate:  "unix",
		CommSpecPath: specPath,
		Enabled:      true,
		Priority:     0,
		Capabilities: []string{"all"},
		ShareMemory:  true,
	})
	writeLocalDefaultSpec(specPath)
}

// writeLocalDefaultSpec drops a minimal comm-spec template for the local node
// when one isn't already present. Best-effort.
func writeLocalDefaultSpec(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	const tmpl = `# local-default node

- wrapper: local
- communicate: unix (in-process)
- share_memory: true (local only)

This node runs sub-agents in the same process as the orchestrator. It is the
default target when no other node is selected. Remote nodes receive only a
bounded context packet and never the memory bus.
`
	_ = os.WriteFile(path, []byte(tmpl), 0o600)
}

// pickNode selects an execution node for a spawn. Order:
//  1. explicit hintName (if enabled)
//  2. highest-priority enabled node serving the role (or "*")
//  3. local-default fallback
//
// It never fails: on any store error it returns a synthetic local-default node
// so spawning always proceeds in-proc (backward-compatible).
func (o *Orchestrator) pickNode(ctx context.Context, role, hintName string) chatstore.Node {
	fallback := chatstore.Node{Name: localDefaultNode, Role: "*", Wrapper: "local", Communicate: "unix", Enabled: true, ShareMemory: true}
	if o == nil || o.chat == nil {
		return fallback
	}
	if h := strings.TrimSpace(hintName); h != "" {
		if n, ok, err := o.chat.GetNode(ctx, h); err == nil && ok && n.Enabled {
			return n
		}
	}
	nodes, err := o.chat.NodesForRole(ctx, role)
	if err == nil {
		for _, n := range nodes {
			return n // already ordered priority desc; first is best
		}
	}
	if n, ok, err := o.chat.GetNode(ctx, localDefaultNode); err == nil && ok {
		return n
	}
	return fallback
}
