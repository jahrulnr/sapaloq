package main

import (
	"encoding/json"
	"runtime"
	"time"

	"github.com/jahrulnr/sapaloq/internal/hostcontext"
)

// ComposeAttachment is metadata the frontend passes for host context (not file bodies).
type ComposeAttachment struct {
	Name  string `json:"name"`
	Path  string `json:"path,omitempty"`
	IsDir bool   `json:"isDir,omitempty"`
}

func buildHostContextJSON(sessionWorkspace string, attachments []ComposeAttachment) json.RawMessage {
	snap := hostcontext.Snapshot{
		Version:    hostcontext.Version,
		CapturedAt: time.Now().UTC(),
		UI: hostcontext.UI{
			Mode:                   "orchestrator",
			ComposeAttachmentCount: len(attachments),
		},
		Host: hostcontext.Host{OS: runtime.GOOS},
	}
	if ws := sessionWorkspace; ws != "" {
		snap.Workspace.SessionWorkspace = ws
		snap.Workspace.ActiveRoot = ws
		snap.Workspace.Roots = []string{ws}
	}
	if len(attachments) > 0 {
		snap.Attachments = make([]hostcontext.Attachment, 0, len(attachments))
		for _, a := range attachments {
			kind := "file"
			if a.IsDir {
				kind = "dir"
			}
			snap.Attachments = append(snap.Attachments, hostcontext.Attachment{
				Path: a.Path,
				Kind: kind,
				Name: a.Name,
			})
		}
	}
	if snap.Workspace.SessionWorkspace == "" && len(snap.Attachments) == 0 {
		return nil
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil
	}
	return raw
}
