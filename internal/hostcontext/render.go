package hostcontext

import (
	"strings"
)

const contractLine = "Authoritative cwd: session_workspace above, else workspace= in runtime variables. Hints only; not durable. Read file bodies via tools unless attached in the user message."

// Render builds the comprehension-first system block for a normalized snapshot.
func Render(s *Snapshot) string {
	if s == nil || s.IsEmpty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("---\n# Host context (ephemeral)\n\n")

	if ws := strings.TrimSpace(s.Workspace.SessionWorkspace); ws != "" {
		b.WriteString("session_workspace=")
		b.WriteString(ws)
		b.WriteByte('\n')
	}
	if root := strings.TrimSpace(s.Workspace.ActiveRoot); root != "" && root != strings.TrimSpace(s.Workspace.SessionWorkspace) {
		b.WriteString("active_root=")
		b.WriteString(root)
		b.WriteByte('\n')
	}

	if s.Editor.ActiveFile != nil && strings.TrimSpace(s.Editor.ActiveFile.Path) != "" {
		b.WriteString("active_file=")
		b.WriteString(s.Editor.ActiveFile.Path)
		if s.Editor.ActiveFile.CursorLine > 0 {
			b.WriteString(" (cursor L")
			b.WriteString(itoa(s.Editor.ActiveFile.CursorLine))
			b.WriteByte(')')
		}
		b.WriteByte('\n')
	} else {
		b.WriteString("active_file=none\n")
	}

	if paths := attachmentPathList(s); len(paths) > 0 {
		b.WriteString("attachment_paths=")
		b.WriteString(strings.Join(paths, ","))
		b.WriteByte('\n')
	}

	if mode := strings.TrimSpace(s.UI.Mode); mode != "" {
		b.WriteString("ui_mode=")
		b.WriteString(mode)
		b.WriteByte('\n')
	}
	if s.UI.ComposeAttachmentCount > 0 {
		b.WriteString("compose_attachment_count=")
		b.WriteString(itoa(s.UI.ComposeAttachmentCount))
		b.WriteByte('\n')
	}
	if osName := strings.TrimSpace(s.Host.OS); osName != "" {
		b.WriteString("host_os=")
		b.WriteString(osName)
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	b.WriteString(contractLine)
	b.WriteString("\n---")

	extra := renderSelectionBlob(s)
	if extra != "" {
		b.WriteByte('\n')
		b.WriteString(extra)
	}
	return b.String()
}

func attachmentPathList(s *Snapshot) []string {
	paths := s.AttachmentPaths()
	if len(paths) > 0 {
		return paths
	}
	return nil
}

func renderSelectionBlob(s *Snapshot) string {
	if s == nil || s.Editor.Selection == nil {
		return ""
	}
	text := strings.TrimSpace(s.Editor.Selection.Text)
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<host_selection>\n")
	b.WriteString(text)
	b.WriteString("\n</host_selection>")
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
