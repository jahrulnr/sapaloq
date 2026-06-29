package orchestrator

import (
	"encoding/json"

	"github.com/jahrulnr/sapaloq/internal/hostcontext"
)

func (o *Orchestrator) setSessionHostContext(sessionID string, raw json.RawMessage) {
	if o == nil || sessionID == "" {
		return
	}
	cfg := o.snapshot().cfg.Orchestrator.HostContext.WithDefaults()
	if !cfg.EnabledOrDefault() {
		return
	}
	parsed := hostcontext.ParseRaw(raw)
	o.hostCtxMu.Lock()
	defer o.hostCtxMu.Unlock()
	if o.sessionHostContext == nil {
		o.sessionHostContext = make(map[string]*hostcontext.Snapshot)
	}
	if parsed == nil {
		delete(o.sessionHostContext, sessionID)
		return
	}
	limits := hostcontext.Limits{
		MaxAttachments:   cfg.MaxAttachments,
		MaxSelectionText: 0,
		MaxRecentFiles:   0,
	}
	norm := hostcontext.Normalize(parsed, limits, o.redactor)
	if norm == nil || norm.IsEmpty() {
		delete(o.sessionHostContext, sessionID)
		return
	}
	o.sessionHostContext[sessionID] = norm
}

func (o *Orchestrator) sessionHostSnapshot(sessionID string) *hostcontext.Snapshot {
	if o == nil || sessionID == "" {
		return nil
	}
	o.hostCtxMu.Lock()
	defer o.hostCtxMu.Unlock()
	if o.sessionHostContext == nil {
		return nil
	}
	return o.sessionHostContext[sessionID]
}

func (o *Orchestrator) clearSessionHostContext(sessionID string) {
	if o == nil || sessionID == "" {
		return
	}
	o.hostCtxMu.Lock()
	defer o.hostCtxMu.Unlock()
	if o.sessionHostContext != nil {
		delete(o.sessionHostContext, sessionID)
	}
}

// hostContextBlock renders the ephemeral widget host snapshot for foreground Ask.
func (o *Orchestrator) hostContextBlock(sessionID string) string {
	if o == nil {
		return ""
	}
	cfg := o.snapshot().cfg.Orchestrator.HostContext.WithDefaults()
	if !cfg.EnabledOrDefault() {
		return ""
	}
	snap := o.sessionHostSnapshot(sessionID)
	if snap == nil || snap.IsEmpty() {
		return ""
	}
	block := hostcontext.Render(snap)
	return truncateHostBlock(block, cfg.MaxBlockTokens)
}

func truncateHostBlock(block string, maxTokens int) string {
	if block == "" || maxTokens <= 0 {
		return block
	}
	maxChars := maxTokens * 4
	if len(block) <= maxChars {
		return block
	}
	return block[:maxChars] + "\n…"
}

// hostContextSearchHints exposes ephemeral host signals for prefetch and skills.
func (o *Orchestrator) hostContextSearchHints(sessionID string) hostcontext.SearchHints {
	if o == nil {
		return hostcontext.SearchHints{}
	}
	cfg := o.snapshot().cfg.Orchestrator.HostContext.WithDefaults()
	if !cfg.EnabledOrDefault() {
		return hostcontext.SearchHints{}
	}
	snap := o.sessionHostSnapshot(sessionID)
	if snap == nil {
		return hostcontext.SearchHints{}
	}
	return snap.SearchHints()
}

// hostContextAttachmentPaths exposes attachment paths for prefetch hints.
func (o *Orchestrator) hostContextAttachmentPaths(sessionID string) []string {
	return o.hostContextSearchHints(sessionID).AttachmentPaths
}

// HostContextSnapshotPresent reports whether a non-empty ephemeral host snapshot
// is stored for the session (set from chat_send host_context).
func (o *Orchestrator) HostContextSnapshotPresent(sessionID string) bool {
	if o == nil || sessionID == "" {
		return false
	}
	snap := o.sessionHostSnapshot(sessionID)
	return snap != nil && !snap.IsEmpty()
}
