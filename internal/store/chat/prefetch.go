package chat

import (
	"context"
	"strings"
	"time"
)

type PrefetchRule struct {
	ID            int64   `json:"id"`
	IntentPattern string  `json:"intent_pattern"`
	Namespace     string  `json:"namespace"`
	FactKinds     string  `json:"fact_kinds"`
	SkillIDs      string  `json:"skill_ids"`
	ConfigKeys    string  `json:"config_keys"`
	HitCount      int64   `json:"hit_count"`
	SuccessCount  int64   `json:"success_count"`
	SuccessRate   float64 `json:"success_rate"`
}

type PrefetchTelemetry struct {
	SessionID     string
	Intent        string
	Confidence    float64
	DeepCheckUsed bool
	TaskSuccess   *bool
	LatencyMS     int64
}

func (s *Store) UpsertPrefetchRule(ctx context.Context, r PrefetchRule) error {
	_ = ctx
	intent := strings.TrimSpace(r.IntentPattern)
	if intent == "" {
		return nil
	}
	ns := strings.TrimSpace(r.Namespace)
	if ns == "" {
		ns = defaultNamespace
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.prefetchRules {
		if s.prefetchRules[i].IntentPattern == intent && s.prefetchRules[i].Namespace == ns {
			s.prefetchRules[i].FactKinds = jsonOrEmptyArray(r.FactKinds)
			s.prefetchRules[i].SkillIDs = jsonOrEmptyArray(r.SkillIDs)
			s.prefetchRules[i].ConfigKeys = jsonOrEmptyArray(r.ConfigKeys)
			return s.savePrefetchRules()
		}
	}
	id := s.nextPrefetchID
	s.nextPrefetchID++
	s.prefetchRules = append(s.prefetchRules, PrefetchRule{
		ID: id, IntentPattern: intent, Namespace: ns,
		FactKinds: jsonOrEmptyArray(r.FactKinds), SkillIDs: jsonOrEmptyArray(r.SkillIDs),
		ConfigKeys: jsonOrEmptyArray(r.ConfigKeys),
	})
	return s.savePrefetchRules()
}

func (s *Store) PrefetchRule(ctx context.Context, intent, namespace string) (PrefetchRule, bool, error) {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return PrefetchRule{}, false, nil
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}
	for _, ns := range dedupeNamespaces(namespace) {
		r, ok, err := s.prefetchRuleExact(ctx, intent, ns)
		if err != nil {
			return PrefetchRule{}, false, err
		}
		if ok {
			return r, true, nil
		}
	}
	return PrefetchRule{}, false, nil
}

func (s *Store) prefetchRuleExact(ctx context.Context, intent, namespace string) (PrefetchRule, bool, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.prefetchRules {
		if r.IntentPattern == intent && r.Namespace == namespace {
			return r, true, nil
		}
	}
	return PrefetchRule{}, false, nil
}

func (s *Store) RecordPrefetchHit(ctx context.Context, intent, namespace string, success bool) error {
	_ = ctx
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return nil
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.prefetchRules {
		if s.prefetchRules[i].IntentPattern == intent && s.prefetchRules[i].Namespace == namespace {
			s.prefetchRules[i].HitCount++
			if success {
				s.prefetchRules[i].SuccessCount++
			}
			if s.prefetchRules[i].HitCount > 0 {
				s.prefetchRules[i].SuccessRate = float64(s.prefetchRules[i].SuccessCount) / float64(s.prefetchRules[i].HitCount)
			}
			return s.savePrefetchRules()
		}
	}
	return nil
}

func (s *Store) LogPrefetch(ctx context.Context, t PrefetchTelemetry) error {
	_ = ctx
	rec := prefetchLogRecord{
		SessionID: t.SessionID, Intent: t.Intent, Confidence: t.Confidence,
		DeepCheckUsed: t.DeepCheckUsed, TaskSuccess: t.TaskSuccess, LatencyMS: t.LatencyMS,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	return appendJSONL(s.paths.prefetchLogFile(), rec)
}

func (s *Store) HotCacheGet(ctx context.Context, key string) (string, bool, error) {
	_ = ctx
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.hotCache[key]
	if !ok {
		return "", false, nil
	}
	if exp := parseFactTime(c.ExpiresAt); !exp.IsZero() && time.Now().UTC().After(exp) {
		delete(s.hotCache, key)
		_ = s.saveHotCache()
		return "", false, nil
	}
	return c.Payload, true, nil
}

func (s *Store) HotCacheSet(ctx context.Context, key, payload string, ttl time.Duration) error {
	_ = ctx
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.hotCache[key] = hotCacheRecord{
		Key: key, Payload: payload,
		ExpiresAt: now.Add(ttl).Format(time.RFC3339Nano),
		CreatedAt: now.Format(time.RFC3339Nano),
	}
	return s.saveHotCache()
}

func (s *Store) PruneHotCache(ctx context.Context) (int64, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	var n int64
	for k, c := range s.hotCache {
		if exp := parseFactTime(c.ExpiresAt); !exp.IsZero() && now.After(exp) {
			delete(s.hotCache, k)
			n++
		}
	}
	if n > 0 {
		_ = s.saveHotCache()
	}
	return n, nil
}

func jsonOrEmptyArray(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "[]"
	}
	return s
}

func dedupeNamespaces(ns string) []string {
	if ns == "" || ns == defaultNamespace {
		return []string{defaultNamespace}
	}
	return []string{ns, defaultNamespace}
}
