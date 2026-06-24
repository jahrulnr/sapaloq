package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	chatstore "github.com/jahrulnr/sapaloq/internal/store/chat"
)

// PrefetchPacket is the bounded result of the index prefetch (Context-SOP Fase
// 1): the classified intent/mode, the facts and skills the index surfaced, and
// the anti-deep-check decision derived from Confidence. It is assembled
// deterministically from SQLite (hot_cache → facts → FTS) - never the
// transcript - so it survives compaction and a process restart.
type PrefetchPacket struct {
	Intent        string           `json:"intent"`
	Mode          string           `json:"mode"`
	Namespace     string           `json:"namespace"`
	Confidence    float64          `json:"confidence"`
	Facts         []chatstore.Fact `json:"facts,omitempty"`
	AntiDeepCheck bool             `json:"anti_deep_check"`
	FromHotCache  bool             `json:"from_hot_cache"`
	rule          *chatstore.PrefetchRule
}

// maxPrefetchFacts bounds how many facts the packet carries so the rendered
// system block stays inside its token budget.
const maxPrefetchFacts = 8

// prefetchContext runs the ingress + index prefetch for a user message. It is
// best-effort and side-effect free aside from a hot_cache write: any store
// error degrades to a low-confidence chat packet rather than failing the turn.
//
// Order (per docs/CONTEXT-SOP.md Fase 1):
//  1. classify intent + mode (no LLM)
//  2. hot_cache lookup (repeat-within-TTL serve)
//  3. prefetch_rules lookup → which fact kinds to load
//  4. facts by namespace (mode-scoped) + FTS over the message keywords
//
// The returned packet's AntiDeepCheck is true when Confidence crosses the
// threshold, which the orchestrator uses to skip filesystem exploration.
func (o *Orchestrator) prefetchContext(ctx context.Context, message string) PrefetchPacket {
	mc := o.cfg.Memory.WithDefaults()
	threshold := mc.PrefetchConfidenceThreshold
	ttl := time.Duration(mc.HotCacheTTLSeconds) * time.Second

	in := classifyIntent(message)
	namespace := in.Mode
	if namespace == "" {
		namespace = "personal"
	}
	packet := PrefetchPacket{
		Intent:     in.Name,
		Mode:       in.Mode,
		Namespace:  namespace,
		Confidence: in.Confidence,
	}
	if o == nil || o.chat == nil {
		packet.AntiDeepCheck = packet.Confidence >= threshold
		return packet
	}

	// Hot cache: serve a recent identical-intent packet to avoid re-querying.
	cacheKey := "prefetch:" + namespace + ":" + in.Name + ":" + hotCacheDigest(message)
	if cached, ok, err := o.chat.HotCacheGet(ctx, cacheKey); err == nil && ok {
		var cp PrefetchPacket
		if json.Unmarshal([]byte(cached), &cp) == nil {
			cp.FromHotCache = true
			cp.AntiDeepCheck = cp.Confidence >= threshold
			return cp
		}
	}

	// Prefetch rule → fact kinds to prioritize. Missing rule is fine (we still
	// load namespace facts + FTS).
	var factKinds []string
	if rule, ok, err := o.chat.PrefetchRule(ctx, in.Name, namespace); err == nil && ok {
		packet.rule = &rule
		factKinds = decodeStringArray(rule.FactKinds)
		// Tiny confidence boost when a learned rule exists for this intent.
		if packet.Confidence < 0.95 {
			packet.Confidence += 0.05
		}
	}

	// Facts: mode-scoped namespace load, optionally narrowed to the rule's
	// kinds, then augmented by an FTS match over the message keywords.
	seen := make(map[int64]struct{})
	add := func(facts []chatstore.Fact) {
		for _, f := range facts {
			if len(packet.Facts) >= maxPrefetchFacts {
				return
			}
			if _, dup := seen[f.ID]; dup {
				continue
			}
			seen[f.ID] = struct{}{}
			packet.Facts = append(packet.Facts, f)
		}
	}
	if len(factKinds) > 0 {
		for _, kind := range factKinds {
			if got, err := o.chat.FactsByNamespace(ctx, namespace, kind, maxPrefetchFacts); err == nil {
				add(got)
			}
		}
	} else {
		if got, err := o.chat.FactsByNamespace(ctx, namespace, "", maxPrefetchFacts); err == nil {
			add(got)
		}
	}
	if strings.TrimSpace(message) != "" && len(packet.Facts) < maxPrefetchFacts {
		// Exclude the skill index from prefetch facts - skills are injected
		// separately by skillsBlock.
		if got, err := o.chat.SearchFacts(ctx, message, nil, maxPrefetchFacts); err == nil {
			filtered := got[:0]
			for _, f := range got {
				if f.Kind == "skill" {
					continue
				}
				filtered = append(filtered, f)
			}
			add(filtered)
		}
	}

	packet.AntiDeepCheck = packet.Confidence >= threshold

	// Cache the assembled packet briefly so an immediate repeat is served fast.
	if payload, err := json.Marshal(packet); err == nil {
		_ = o.chat.HotCacheSet(ctx, cacheKey, string(payload), ttl)
	}
	return packet
}

// render builds the bounded system-prompt block for a packet, or "" when there
// is nothing useful to inject (no facts and low confidence). The block is kept
// short (a few facts) to protect the token budget; the anti-deep-check line is
// a directive the model honors when confidence is high.
func (p PrefetchPacket) render() string {
	if len(p.Facts) == 0 && !p.AntiDeepCheck {
		return ""
	}
	var b strings.Builder
	b.WriteString("Prefetched context (from memory index - trust this before exploring):")
	if len(p.Facts) > 0 {
		for _, f := range p.Facts {
			b.WriteString("\n- ")
			if f.Kind != "" {
				b.WriteString("[")
				b.WriteString(f.Kind)
				b.WriteString("] ")
			}
			if f.Key != "" {
				b.WriteString(f.Key)
				b.WriteString(": ")
			}
			line := f.Value
			if line == "" {
				line = f.Content
			}
			b.WriteString(collapseSpaces(line))
		}
	}
	if p.AntiDeepCheck {
		b.WriteString("\nThe above is high-confidence; do not search the filesystem or re-read skills/memory before acting unless it is clearly insufficient.")
	}
	return b.String()
}

// hotCacheDigest produces a short, stable key fragment from a message so the
// same prompt maps to the same cache entry without storing the full text.
func hotCacheDigest(message string) string {
	msg := strings.ToLower(strings.TrimSpace(message))
	if len(msg) > 64 {
		msg = msg[:64]
	}
	return msg
}

// decodeStringArray parses a JSON string array, returning nil on any error or
// empty input (the caller treats nil as "no kinds filter").
func decodeStringArray(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(s), &out) != nil {
		return nil
	}
	return out
}

// collapseSpaces flattens internal whitespace/newlines to single spaces and
// truncates over-long lines so one fact can't blow the prefetch budget.
func collapseSpaces(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
