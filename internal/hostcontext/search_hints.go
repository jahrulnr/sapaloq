package hostcontext

import (
	"path/filepath"
	"strings"
)

// SearchHints are ephemeral host signals used to augment index prefetch FTS and
// skillsBlock matching. Persisted cwd is workspace_set IPC only; hints here are ephemeral.
type SearchHints struct {
	SessionWorkspace string
	AttachmentPaths  []string
}

// SearchHints extracts prefetch/skill search signals from a normalized snapshot.
func (s *Snapshot) SearchHints() SearchHints {
	if s == nil {
		return SearchHints{}
	}
	return SearchHints{
		SessionWorkspace: strings.TrimSpace(s.Workspace.SessionWorkspace),
		AttachmentPaths:  s.AttachmentPaths(),
	}
}

// PrefetchSearchQuery builds the FTS query for prefetch: user message plus host
// workspace and attachment paths.
func PrefetchSearchQuery(message string, hints SearchHints) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(message))
	if ws := strings.TrimSpace(hints.SessionWorkspace); ws != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(ws)
	}
	for _, p := range hints.AttachmentPaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(p)
	}
	return b.String()
}

// SkillsAugmentQuery returns deduped tokens from attachment paths joined for
// trigger matching (full path, basename, extension).
func SkillsAugmentQuery(hints SearchHints) string {
	return strings.Join(SkillsSearchTerms(hints), " ")
}

// SkillsSearchTerms returns deduped attachment-path tokens for per-term FTS.
func SkillsSearchTerms(hints SearchHints) []string {
	if len(hints.AttachmentPaths) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var tokens []string
	add := func(tok string) {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			return
		}
		if _, ok := seen[tok]; ok {
			return
		}
		seen[tok] = struct{}{}
		tokens = append(tokens, tok)
	}
	for _, p := range hints.AttachmentPaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		add(p)
		add(filepath.Base(p))
		add(filepath.Ext(p))
		base := filepath.Base(p)
		if ext := filepath.Ext(base); ext != "" {
			add(strings.TrimSuffix(base, ext))
		}
	}
	return tokens
}

// CacheDigest returns a stable hot-cache key fragment for host hints independent
// of message truncation.
func (h SearchHints) CacheDigest() string {
	var parts []string
	if ws := hotCacheDigest(h.SessionWorkspace); ws != "" {
		parts = append(parts, "ws:"+ws)
	}
	if len(h.AttachmentPaths) > 0 {
		if digest := hotCacheDigest(strings.Join(h.AttachmentPaths, "\n")); digest != "" {
			parts = append(parts, "ap:"+digest)
		}
	}
	return strings.Join(parts, ":")
}

func hotCacheDigest(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}
