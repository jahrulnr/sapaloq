package chat

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Node is one execution target for sub-agents: a local in-proc wrapper or a
// remote endpoint reached over a transport. See docs/NODES.md.
type Node struct {
	Name         string    `json:"name"`
	Role         string    `json:"role"`        // role this node serves, or "*" for any
	Wrapper      string    `json:"wrapper"`     // local | remote
	Address      string    `json:"address"`     // unix socket path or ws/https URL
	Communicate  string    `json:"communicate"` // unix | ws | http
	CommSpecPath string    `json:"comm_spec_path"`
	Enabled      bool      `json:"enabled"`
	Priority     int       `json:"priority"`
	Capabilities []string  `json:"capabilities"`
	ShareMemory  bool      `json:"share_memory"`
	LastSeenAt   string    `json:"last_seen_at,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IsLocal reports whether the node runs in-process (no transport).
func (n Node) IsLocal() bool {
	return strings.EqualFold(n.Wrapper, "local") || strings.EqualFold(n.Communicate, "unix")
}

// UpsertNode inserts or replaces a node by name.
func (s *Store) UpsertNode(ctx context.Context, n Node) error {
	n.Name = strings.TrimSpace(n.Name)
	if n.Name == "" {
		return nil
	}
	if n.Role == "" {
		n.Role = "*"
	}
	if n.Wrapper == "" {
		n.Wrapper = "local"
	}
	if n.Communicate == "" {
		n.Communicate = "unix"
	}
	caps, _ := json.Marshal(n.Capabilities)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	created := now
	if existing, ok, _ := s.GetNode(ctx, n.Name); ok && !existing.CreatedAt.IsZero() {
		created = existing.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO nodes(name, role, wrapper, address, communicate, comm_spec_path, enabled, priority, capabilities, share_memory, last_seen_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			role=excluded.role,
			wrapper=excluded.wrapper,
			address=excluded.address,
			communicate=excluded.communicate,
			comm_spec_path=excluded.comm_spec_path,
			enabled=excluded.enabled,
			priority=excluded.priority,
			capabilities=excluded.capabilities,
			share_memory=excluded.share_memory,
			updated_at=excluded.updated_at`,
		n.Name, n.Role, n.Wrapper, n.Address, n.Communicate, n.CommSpecPath,
		boolToInt(n.Enabled), n.Priority, string(caps), boolToInt(n.ShareMemory),
		n.LastSeenAt, n.LastError, created, now,
	)
	return err
}

// GetNode returns a node by name.
func (s *Store) GetNode(ctx context.Context, name string) (Node, bool, error) {
	rows, err := s.queryNodes(ctx, `WHERE name=?`, strings.TrimSpace(name))
	if err != nil {
		return Node{}, false, err
	}
	if len(rows) == 0 {
		return Node{}, false, nil
	}
	return rows[0], true, nil
}

// ListNodes returns all nodes ordered by priority desc, name asc.
func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	return s.queryNodes(ctx, `ORDER BY priority DESC, name ASC`)
}

// NodesForRole returns enabled nodes that serve role (or "*"), highest priority
// first. local-default (role "*") is naturally included as a fallback.
func (s *Store) NodesForRole(ctx context.Context, role string) ([]Node, error) {
	role = strings.TrimSpace(role)
	return s.queryNodes(ctx, `WHERE enabled=1 AND (role=? OR role='*') ORDER BY priority DESC, name ASC`, role)
}

// SetNodeEnabled toggles a node's enabled flag.
func (s *Store) SetNodeEnabled(ctx context.Context, name string, enabled bool) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET enabled=?, updated_at=? WHERE name=?`, boolToInt(enabled), now, strings.TrimSpace(name))
	return err
}

// TouchNode records a heartbeat / last error for a node.
func (s *Store) TouchNode(ctx context.Context, name, lastErr string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET last_seen_at=?, last_error=?, updated_at=? WHERE name=?`, now, lastErr, now, strings.TrimSpace(name))
	return err
}

func (s *Store) queryNodes(ctx context.Context, where string, args ...any) ([]Node, error) {
	sql := `SELECT name, role, wrapper, address, communicate, comm_spec_path, enabled, priority, capabilities, share_memory, last_seen_at, last_error, created_at, updated_at FROM nodes `
	if where != "" {
		sql += where
	}
	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		var enabled, shareMem int
		var caps, created, updated string
		if err := rows.Scan(&n.Name, &n.Role, &n.Wrapper, &n.Address, &n.Communicate, &n.CommSpecPath,
			&enabled, &n.Priority, &caps, &shareMem, &n.LastSeenAt, &n.LastError, &created, &updated); err != nil {
			return nil, err
		}
		n.Enabled = enabled == 1
		n.ShareMemory = shareMem == 1
		if caps != "" {
			_ = json.Unmarshal([]byte(caps), &n.Capabilities)
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			n.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
			n.UpdatedAt = t
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
