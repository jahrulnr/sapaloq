package chat

import (
	"context"
	"strings"
	"time"
)

type Node struct {
	Name         string    `json:"name"`
	Role         string    `json:"role"`
	Wrapper      string    `json:"wrapper"`
	Address      string    `json:"address"`
	Communicate  string    `json:"communicate"`
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

func (n Node) IsLocal() bool {
	return strings.EqualFold(n.Wrapper, "local") || strings.EqualFold(n.Communicate, "unix")
}

func (s *Store) UpsertNode(ctx context.Context, n Node) error {
	_ = ctx
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
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.nodes {
		if s.nodes[i].Name == n.Name {
			if !n.CreatedAt.IsZero() {
				n.CreatedAt = s.nodes[i].CreatedAt
			} else {
				n.CreatedAt = s.nodes[i].CreatedAt
			}
			n.UpdatedAt = now
			s.nodes[i] = n
			return s.saveNodes()
		}
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now
	s.nodes = append(s.nodes, n)
	return s.saveNodes()
}

func (s *Store) GetNode(ctx context.Context, name string) (Node, bool, error) {
	rows, err := s.queryNodes(ctx, name)
	if err != nil || len(rows) == 0 {
		return Node{}, false, err
	}
	return rows[0], true, nil
}

func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	return s.queryNodes(ctx, "")
}

func (s *Store) NodesForRole(ctx context.Context, role string) ([]Node, error) {
	role = strings.TrimSpace(role)
	all, err := s.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	var out []Node
	for _, n := range all {
		if n.Enabled && (n.Role == role || n.Role == "*") {
			out = append(out, n)
		}
	}
	return out, nil
}

func (s *Store) SetNodeEnabled(ctx context.Context, name string, enabled bool) error {
	_ = ctx
	name = strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for i := range s.nodes {
		if s.nodes[i].Name == name {
			s.nodes[i].Enabled = enabled
			s.nodes[i].UpdatedAt = now
			return s.saveNodes()
		}
	}
	return nil
}

func (s *Store) TouchNode(ctx context.Context, name, lastErr string) error {
	_ = ctx
	name = strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for i := range s.nodes {
		if s.nodes[i].Name == name {
			s.nodes[i].LastSeenAt = now.Format(time.RFC3339Nano)
			s.nodes[i].LastError = lastErr
			s.nodes[i].UpdatedAt = now
			return s.saveNodes()
		}
	}
	return nil
}

func (s *Store) queryNodes(ctx context.Context, name string) ([]Node, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Node
	for _, n := range s.nodes {
		if name != "" && n.Name != name {
			continue
		}
		out = append(out, n)
	}
	// sort priority desc, name asc
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Priority > out[i].Priority || (out[j].Priority == out[i].Priority && out[j].Name < out[i].Name) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}
