package chat

import (
	"context"
	"strings"
	"time"
)

// Fact is a durable memory fact.
type Fact struct {
	ID         int64      `json:"id"`
	Namespace  string     `json:"namespace,omitempty"`
	Kind       string     `json:"kind"`
	Key        string     `json:"key,omitempty"`
	Value      string     `json:"value,omitempty"`
	Content    string     `json:"content"`
	Confidence float64    `json:"confidence,omitempty"`
	ObsoleteAt *time.Time `json:"obsolete_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

const defaultNamespace = "default"

func (s *Store) AddFact(ctx context.Context, kind, content string) (int64, error) {
	_ = ctx
	kind = strings.TrimSpace(kind)
	content = strings.TrimSpace(content)
	if kind == "" || content == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	id := s.nextFactID
	s.nextFactID++
	f := Fact{
		ID: id, Namespace: defaultNamespace, Kind: kind, Content: content,
		Confidence: 1.0, CreatedAt: now, UpdatedAt: &now,
	}
	s.facts = append(s.facts, f)
	return id, s.saveFacts()
}

func (s *Store) UpsertFact(ctx context.Context, namespace, kind, key, value, content string, confidence float64) (int64, error) {
	_ = ctx
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	content = strings.TrimSpace(content)
	if kind == "" {
		return 0, nil
	}
	if content == "" {
		content = strings.TrimSpace(key + " " + value)
	}
	if content == "" {
		return 0, nil
	}
	if confidence <= 0 {
		confidence = 1.0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if key != "" {
		for i := range s.facts {
			f := &s.facts[i]
			if f.Namespace == namespace && f.Kind == kind && f.Key == key && f.ObsoleteAt == nil {
				f.Value = value
				f.Content = content
				f.Confidence = confidence
				f.ObsoleteAt = nil
				f.UpdatedAt = &now
				return f.ID, s.saveFacts()
			}
		}
	}
	id := s.nextFactID
	s.nextFactID++
	s.facts = append(s.facts, Fact{
		ID: id, Namespace: namespace, Kind: kind, Key: key, Value: value,
		Content: content, Confidence: confidence, CreatedAt: now, UpdatedAt: &now,
	})
	return id, s.saveFacts()
}

func (s *Store) ObsoleteFact(ctx context.Context, id int64) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for i := range s.facts {
		if s.facts[i].ID == id && s.facts[i].ObsoleteAt == nil {
			s.facts[i].ObsoleteAt = &now
			s.facts[i].UpdatedAt = &now
			return s.saveFacts()
		}
	}
	return nil
}

func (s *Store) FactsByNamespace(ctx context.Context, namespace, kind string, limit int) ([]Fact, error) {
	_ = ctx
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}
	if limit <= 0 {
		limit = 20
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Fact
	for i := len(s.facts) - 1; i >= 0 && len(out) < limit; i-- {
		f := s.facts[i]
		if f.ObsoleteAt != nil || f.Namespace != namespace {
			continue
		}
		if k := strings.TrimSpace(kind); k != "" && f.Kind != k {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *Store) SearchFacts(ctx context.Context, query string, kinds []string, limit int) ([]Fact, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return s.RecentFacts(ctx, "", limit)
	}
	return s.searchFactsLike(ctx, query, kinds, limit)
}

func (s *Store) searchFactsLike(ctx context.Context, query string, kinds []string, limit int) ([]Fact, error) {
	_ = ctx
	if limit <= 0 {
		limit = 20
	}
	kindSet := map[string]bool{}
	for _, k := range kinds {
		if k = strings.TrimSpace(k); k != "" {
			kindSet[k] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Fact
	q := strings.ToLower(query)
	for i := len(s.facts) - 1; i >= 0 && len(out) < limit; i-- {
		f := s.facts[i]
		if f.ObsoleteAt != nil {
			continue
		}
		if len(kindSet) > 0 && !kindSet[f.Kind] {
			continue
		}
		if strings.Contains(strings.ToLower(f.Content), q) {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *Store) RecentFacts(ctx context.Context, kind string, limit int) ([]Fact, error) {
	_ = ctx
	if limit <= 0 {
		limit = 20
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Fact
	k := strings.TrimSpace(kind)
	for i := len(s.facts) - 1; i >= 0 && len(out) < limit; i-- {
		f := s.facts[i]
		if f.ObsoleteAt != nil {
			continue
		}
		if k != "" && f.Kind != k {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *Store) DeleteFact(ctx context.Context, id int64) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []Fact
	for _, f := range s.facts {
		if f.ID != id {
			kept = append(kept, f)
		}
	}
	s.facts = kept
	return s.saveFacts()
}

func parseFactTime(s string) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return parsed
	}
	if parsed, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return parsed
	}
	return time.Time{}
}
