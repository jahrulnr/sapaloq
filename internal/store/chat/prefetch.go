package chat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// PrefetchRule maps an intent to the fact kinds / skills / config keys the
// ingress pipeline should prefetch, plus bandit-style telemetry used for rule
// tuning. The *IDs/*Keys fields are stored as JSON arrays in the DB; this DAO
// keeps them as opaque JSON strings so the orchestrator owns the encoding.
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

// PrefetchTelemetry is one ingress-pipeline observation appended to
// prefetch_log for later rule tuning.
type PrefetchTelemetry struct {
	SessionID     string
	Intent        string
	Confidence    float64
	DeepCheckUsed bool
	TaskSuccess   *bool
	LatencyMS     int64
}

// UpsertPrefetchRule inserts or updates a rule keyed on (intent_pattern,
// namespace). Telemetry counters (hit/success) are preserved on update; only
// the mapping columns are refreshed.
func (s *Store) UpsertPrefetchRule(ctx context.Context, r PrefetchRule) error {
	intent := strings.TrimSpace(r.IntentPattern)
	if intent == "" {
		return nil
	}
	ns := strings.TrimSpace(r.Namespace)
	if ns == "" {
		ns = defaultNamespace
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO prefetch_rules(intent_pattern, namespace, fact_kinds, skill_ids, config_keys, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(intent_pattern, namespace) DO UPDATE SET
			fact_kinds=excluded.fact_kinds,
			skill_ids=excluded.skill_ids,
			config_keys=excluded.config_keys,
			updated_at=excluded.updated_at`,
		intent, ns, jsonOrEmptyArray(r.FactKinds), jsonOrEmptyArray(r.SkillIDs), jsonOrEmptyArray(r.ConfigKeys), now)
	return err
}

// PrefetchRule returns the rule for an (intent, namespace), trying the exact
// namespace first then falling back to the "default" namespace. ok is false
// when no rule matches.
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
	var r PrefetchRule
	err := s.db.QueryRowContext(ctx, `
		SELECT id, intent_pattern, namespace, fact_kinds, skill_ids, config_keys, hit_count, success_count, success_rate
		FROM prefetch_rules WHERE intent_pattern=? AND namespace=?`,
		intent, namespace).Scan(&r.ID, &r.IntentPattern, &r.Namespace, &r.FactKinds, &r.SkillIDs, &r.ConfigKeys, &r.HitCount, &r.SuccessCount, &r.SuccessRate)
	if errors.Is(err, sql.ErrNoRows) {
		return PrefetchRule{}, false, nil
	}
	if err != nil {
		return PrefetchRule{}, false, err
	}
	return r, true, nil
}

// RecordPrefetchHit bumps the hit counter (and success counter when success is
// true) for a rule and recomputes success_rate. A no-op for an unknown rule.
func (s *Store) RecordPrefetchHit(ctx context.Context, intent, namespace string, success bool) error {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return nil
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	inc := 0
	if success {
		inc = 1
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE prefetch_rules
		SET hit_count = hit_count + 1,
			success_count = success_count + ?,
			success_rate = CAST(success_count + ? AS REAL) / CAST(hit_count + 1 AS REAL),
			updated_at = ?
		WHERE intent_pattern=? AND namespace=?`,
		inc, inc, now, intent, namespace)
	return err
}

// LogPrefetch appends one prefetch telemetry row. Best-effort by convention at
// the call site; returns the underlying error for callers that want it.
func (s *Store) LogPrefetch(ctx context.Context, t PrefetchTelemetry) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var success any
	if t.TaskSuccess != nil {
		if *t.TaskSuccess {
			success = 1
		} else {
			success = 0
		}
	}
	deep := 0
	if t.DeepCheckUsed {
		deep = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO prefetch_log(session_id, intent, confidence, deep_check_used, task_success, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.SessionID, t.Intent, t.Confidence, deep, success, t.LatencyMS, now)
	return err
}

// HotCacheGet returns the payload for a key when present and not expired.
// Expired rows are pruned lazily. ok is false on miss/expiry.
func (s *Store) HotCacheGet(ctx context.Context, key string) (string, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false, nil
	}
	var payload, expires string
	err := s.db.QueryRowContext(ctx, `SELECT payload, expires_at FROM hot_cache WHERE cache_key=?`, key).Scan(&payload, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if exp := parseFactTime(expires); !exp.IsZero() && time.Now().UTC().After(exp) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM hot_cache WHERE cache_key=?`, key)
		return "", false, nil
	}
	return payload, true, nil
}

// HotCacheSet stores a payload under key with a time-to-live. A non-positive
// ttl is treated as a short default so a caller can't accidentally store a
// never-expiring row.
func (s *Store) HotCacheSet(ctx context.Context, key, payload string, ttl time.Duration) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO hot_cache(cache_key, payload, expires_at, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET payload=excluded.payload, expires_at=excluded.expires_at`,
		key, payload, now.Add(ttl).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	return err
}

// PruneHotCache deletes all expired hot_cache rows and returns how many were
// removed. Safe to call periodically; lazy expiry in HotCacheGet covers the
// read path regardless.
func (s *Store) PruneHotCache(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `DELETE FROM hot_cache WHERE expires_at <= ?`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// jsonOrEmptyArray normalizes an empty/whitespace JSON field to "[]" so the
// column is always valid JSON.
func jsonOrEmptyArray(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "[]"
	}
	return s
}

// dedupeNamespaces returns the lookup order for a namespace: the given one,
// then "default" if different.
func dedupeNamespaces(ns string) []string {
	if ns == "" || ns == defaultNamespace {
		return []string{defaultNamespace}
	}
	return []string{ns, defaultNamespace}
}
