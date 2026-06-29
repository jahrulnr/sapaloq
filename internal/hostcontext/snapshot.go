package hostcontext

import "time"

const Version = 1

// Snapshot is the versioned host/IDE context payload the widget publishes per
// chat_send. It is ephemeral: orchestrator stores it in memory only for the
// current turn assembly.
type Snapshot struct {
	Version    int        `json:"version"`
	CapturedAt time.Time  `json:"captured_at,omitempty"`
	UI         UI         `json:"ui,omitempty"`
	Workspace  Workspace  `json:"workspace,omitempty"`
	Editor     Editor     `json:"editor,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Host       Host       `json:"host,omitempty"`
}

type UI struct {
	Mode                   string `json:"mode,omitempty"`
	ComposeAttachmentCount int    `json:"compose_attachment_count,omitempty"`
}

type Workspace struct {
	SessionWorkspace string   `json:"session_workspace,omitempty"`
	Roots            []string `json:"roots,omitempty"`
	ActiveRoot       string   `json:"active_root,omitempty"`
}

type Editor struct {
	ActiveFile   *FileRef   `json:"active_file,omitempty"`
	RecentFiles  []FileRef  `json:"recent_files,omitempty"`
	Selection    *Selection `json:"selection,omitempty"`
}

type FileRef struct {
	Path      string `json:"path,omitempty"`
	Language  string `json:"language,omitempty"`
	LineCount int    `json:"line_count,omitempty"`
	CursorLine int   `json:"cursor_line,omitempty"`
}

type Selection struct {
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Text      string `json:"text,omitempty"`
}

type Attachment struct {
	Path string `json:"path,omitempty"`
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
}

type Host struct {
	OS            string `json:"os,omitempty"`
	WidgetVersion string `json:"widget_version,omitempty"`
}

// Limits caps applied during Normalize.
type Limits struct {
	MaxAttachments   int
	MaxSelectionText int
	MaxRecentFiles   int
}

// DefaultLimits returns v1 widget defaults from the plan.
func DefaultLimits() Limits {
	return Limits{
		MaxAttachments:   20,
		MaxSelectionText: 0,
		MaxRecentFiles:   0,
	}
}
