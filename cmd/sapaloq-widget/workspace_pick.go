package main

import (
	"errors"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// PickWorkspaceFolder opens the native GTK directory chooser (Nautilus-style on GNOME).
// Hidden folders are visible in the dialog. Returns "" when the user cancels.
func (a *App) PickWorkspaceFolder(startDir string) (string, error) {
	if a.ctx == nil {
		return "", errors.New("app not ready")
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Pilih workspace",
		DefaultDirectory: normalizeWorkspaceStartDir(startDir),
		ShowHiddenFiles:  true,
	})
}
