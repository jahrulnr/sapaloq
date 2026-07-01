package hostcontext

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/privacyfilter"
)

// Normalize applies caps, cleans paths, and redacts sensitive selection text.
func Normalize(s *Snapshot, limits Limits, redactor *privacyfilter.Filter) *Snapshot {
	if s == nil {
		return nil
	}
	if limits.MaxAttachments <= 0 {
		limits = DefaultLimits()
	}
	out := *s
	if out.CapturedAt.IsZero() {
		out.CapturedAt = time.Now().UTC()
	}
	out.Version = Version

	out.Workspace.SessionWorkspace = cleanAbs(out.Workspace.SessionWorkspace)
	if out.Workspace.SessionWorkspace != "" {
		out.Workspace.ActiveRoot = out.Workspace.SessionWorkspace
		if len(out.Workspace.Roots) == 0 {
			out.Workspace.Roots = []string{out.Workspace.SessionWorkspace}
		}
	}
	out.Workspace.Roots = capCleanPaths(out.Workspace.Roots, 8)

	if out.Editor.ActiveFile != nil {
		out.Editor.ActiveFile.Path = cleanPath(out.Editor.ActiveFile.Path)
		if out.Editor.ActiveFile.Path == "" {
			out.Editor.ActiveFile = nil
		}
	}
	if limits.MaxRecentFiles <= 0 {
		out.Editor.RecentFiles = nil
	} else {
		out.Editor.RecentFiles = capFileRefs(out.Editor.RecentFiles, limits.MaxRecentFiles)
	}

	atts := make([]Attachment, 0, len(out.Attachments))
	for _, a := range out.Attachments {
		a.Path = cleanAbs(a.Path)
		a.Name = strings.TrimSpace(a.Name)
		a.Kind = strings.TrimSpace(a.Kind)
		if a.Kind == "" {
			if a.Path != "" {
				a.Kind = "file"
			}
		}
		if a.Path == "" && a.Name == "" {
			continue
		}
		atts = append(atts, a)
		if len(atts) >= limits.MaxAttachments {
			break
		}
	}
	out.Attachments = atts

	if out.UI.ComposeAttachmentCount == 0 && len(atts) > 0 {
		out.UI.ComposeAttachmentCount = len(atts)
	}
	if strings.TrimSpace(out.UI.Mode) == "" {
		out.UI.Mode = "orchestrator"
	} else if out.UI.Mode == "ask" {
		out.UI.Mode = "orchestrator"
	}

	if out.Editor.Selection != nil && redactor != nil && limits.MaxSelectionText > 0 {
		text := out.Editor.Selection.Text
		if len(text) > limits.MaxSelectionText {
			text = text[:limits.MaxSelectionText]
		}
		out.Editor.Selection.Text = redactor.Redact(text).Redacted
	} else {
		out.Editor.Selection = nil
	}

	out.Host.OS = strings.TrimSpace(out.Host.OS)
	out.Host.WidgetVersion = strings.TrimSpace(out.Host.WidgetVersion)
	return &out
}

func cleanAbs(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

func capCleanPaths(paths []string, max int) []string {
	if max <= 0 || len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = cleanAbs(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
		if len(out) >= max {
			break
		}
	}
	return out
}

func capFileRefs(refs []FileRef, max int) []FileRef {
	if max <= 0 || len(refs) == 0 {
		return nil
	}
	out := make([]FileRef, 0, len(refs))
	for _, r := range refs {
		r.Path = cleanPath(r.Path)
		if r.Path == "" {
			continue
		}
		out = append(out, r)
		if len(out) >= max {
			break
		}
	}
	return out
}

// IsEmpty reports whether a normalized snapshot has nothing useful to inject.
func (s *Snapshot) IsEmpty() bool {
	if s == nil {
		return true
	}
	if s.Workspace.SessionWorkspace != "" {
		return false
	}
	if len(s.Attachments) > 0 {
		return false
	}
	if s.Editor.ActiveFile != nil {
		return false
	}
	if len(s.Editor.RecentFiles) > 0 {
		return false
	}
	if s.Editor.Selection != nil {
		return false
	}
	if s.UI.ComposeAttachmentCount > 0 {
		return false
	}
	return true
}
