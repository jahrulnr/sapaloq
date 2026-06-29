package hostcontext

import (
	"encoding/json"
	"strings"
)

const maxRawBytes = 8 * 1024

// ParseRaw decodes widget IPC host_context JSON. Invalid or unsupported payloads
// return nil (degraded mode — chat continues without host block).
func ParseRaw(raw json.RawMessage) *Snapshot {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxRawBytes {
		raw = raw[:maxRawBytes]
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil
	}
	if snap.Version != 0 && snap.Version != Version {
		return nil
	}
	if snap.Version == 0 {
		snap.Version = Version
	}
	return &snap
}

// AttachmentPaths returns deduped absolute paths from attachments for prefetch hints.
func (s *Snapshot) AttachmentPaths() []string {
	if s == nil || len(s.Attachments) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(s.Attachments))
	out := make([]string, 0, len(s.Attachments))
	for _, a := range s.Attachments {
		p := strings.TrimSpace(a.Path)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
