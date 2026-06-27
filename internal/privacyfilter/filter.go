// Package privacyfilter redacts secrets/credentials from text before it is
// shown to the model or written to logs.
//
// It is a secrets-only vendored subset of github.com/packyme/privacy-filter
// (MIT, Copyright (c) 2026 PackyMe). The PII layer and the gitleaks-TOML loader
// from the upstream project were removed: SapaLOQ only redacts secrets (the PII
// layer — email/phone/IP — is intentionally left intact, see docs/ORCHESTRATOR.md)
// and always uses the built-in rule set, so there is no external dependency.
//
// Pure regex, no model, O(n). A Filter is read-only after construction and is
// safe for concurrent reuse. SapaLOQ applies it to every tool result so that, even
// if the model is tricked by an injected instruction into reading a secret, the
// secret is scrubbed before it reaches the model, the logs, or any egress. The AI
// keeps full access to every tool — only sensitive values in results are masked.
package privacyfilter

import (
	"sort"
	"strings"
)

// Entity is one redacted hit. Start/End are UTF-8 byte offsets into the input.
type Entity struct {
	Type  string `json:"type"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

// Result is the outcome of a single redaction pass.
type Result struct {
	Redacted string   `json:"redacted"`
	Hit      bool     `json:"hit"`
	Count    int      `json:"count"`
	Entities []Entity `json:"entities"`
}

// span is an interval produced by a detection layer.
type span struct {
	start, end int
	label      string
}

// Filter holds the compiled rules. Read-only after New; safe for concurrent reuse.
type Filter struct {
	secrets *secretDetector
}

// New builds a Filter using the built-in secret rule set. It never fails.
func New() *Filter {
	return &Filter{secrets: newSecretDetector()}
}

// Stats reports how many rules are loaded (skipped is always 0 for the built-in set).
func (f *Filter) Stats() (rules, skipped int) {
	return len(f.secrets.rules), f.secrets.skipped
}

// Redact detects and masks secrets in text. Concurrency-safe.
func (f *Filter) Redact(text string) Result {
	spans := f.secrets.detect(text) // secrets / credentials only

	merged := mergeSpans(spans)

	// Single-pass rebuild: merged is sorted by start and non-overlapping, O(n).
	var b strings.Builder
	prev := 0
	for _, s := range merged {
		b.WriteString(text[prev:s.start])
		b.WriteString(s.label)
		prev = s.end
	}
	b.WriteString(text[prev:])
	out := b.String()

	entities := make([]Entity, len(merged))
	for i, s := range merged {
		entities[i] = Entity{Type: s.label, Start: s.start, End: s.end, Text: text[s.start:s.end]}
	}
	return Result{
		Redacted: out,
		Hit:      len(merged) > 0,
		Count:    len(merged),
		Entities: entities,
	}
}

// mergeSpans drops invalid/overlapping intervals: sort by start ascending,
// prefer the longer span on a tie, then greedily keep non-overlapping spans.
func mergeSpans(spans []span) []span {
	var valid []span
	for _, s := range spans {
		if s.start >= 0 && s.start < s.end {
			valid = append(valid, s)
		}
	}
	sort.SliceStable(valid, func(i, j int) bool {
		if valid[i].start != valid[j].start {
			return valid[i].start < valid[j].start
		}
		return valid[i].end > valid[j].end
	})
	merged := make([]span, 0, len(valid))
	lastEnd := -1
	for _, s := range valid {
		if s.start >= lastEnd {
			merged = append(merged, s)
			lastEnd = s.end
		}
	}
	return merged
}
