package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// appIcon is shared by the live Linux window and the macOS About panel.
// Packaged macOS/Windows builds also consume build/appicon.png and
// build/windows/icon.ico through Wails' platform packagers.
//
//go:embed build/appicon.png
var appIcon []byte

func main() {
	app := NewApp()

	// Set the GTK program name + WM_CLASS before Wails creates the window.
	// Wails doesn't set WM_CLASS on Linux, so without this GNOME can't match
	// the window to sapaloq.desktop and falls back to a generic taskbar icon
	// (and shows the dev binary name as the title). No-op on non-Linux.
	setProgramClass("sapaloq")

	err := wails.Run(&options.App{
		Title:             "SapaLOQ",
		Width:             76,
		Height:            76,
		MinWidth:          76,
		MinHeight:         76,
		MaxWidth:          760,
		MaxHeight:         860,
		DisableResize:     true,
		Frameless:         true,
		AlwaysOnTop:       true,
		StartHidden:       false,
		HideWindowOnClose: false,
		BackgroundColour:  &options.RGBA{R: 0, G: 0, B: 0, A: 0},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
			// On WebKitGTK the native file-drop signals (drag-data-received /
			// drag-drop) only fire while the webview keeps its built-in GTK drag
			// destination. Wails' DisableWebViewDrop calls gtk_drag_dest_unset()
			// and never re-registers one, which silently kills OnFileDrop. Keep
			// the webview drop enabled so native desktop drops are delivered.
			DisableWebViewDrop: false,
			CSSDropProperty:    "--wails-drop-target",
			CSSDropValue:       "drop",
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		OnDomReady: func(ctx context.Context) {
			// Defer input-shape until window is realized & has correct size
			scheduleInputShape(true)
		},
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
			About: &mac.AboutInfo{
				Title:   "SapaLOQ",
				Message: "Local desktop companion",
				Icon:    appIcon,
			},
		},
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			WindowClassName:      "SapaLOQWidget",
		},
		Linux: &linux.Options{
			WindowIsTranslucent: true,
			WebviewGpuPolicy:    linux.WebviewGpuPolicyAlways,
			Icon:                appIcon,
			ProgramName:         "sapaloq",
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
