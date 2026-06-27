package main

import "fmt"

type workspaceSetResult struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"session_id,omitempty"`
	Path      string `json:"path,omitempty"`
	Message   string `json:"message,omitempty"`
}

func setWorkspace(socketPath, sessionID, path string) (workspaceSetResult, error) {
	responses, err := roundTrip(socketPath, ipcRequest{Op: "workspace_set", SessionID: sessionID, Path: path})
	if err != nil {
		return workspaceSetResult{}, err
	}
	if len(responses) == 0 || !responses[0].OK {
		msg := "core error"
		if len(responses) > 0 && responses[0].Message != "" {
			msg = responses[0].Message
		}
		return workspaceSetResult{OK: false, Message: msg}, fmt.Errorf("%s", msg)
	}
	return workspaceSetResult{
		OK:        true,
		SessionID: responses[0].SessionID,
		Path:      responses[0].Path,
	}, nil
}
