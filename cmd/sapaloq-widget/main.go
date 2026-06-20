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

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:             "sapaloq-widget",
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
		OnStartup: app.startup,
		OnDomReady: func(ctx context.Context) {
			// Defer input-shape until window is realized & has correct size
			scheduleInputShape(true)
		},
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		},
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
		},
		Linux: &linux.Options{
			WindowIsTranslucent: true,
			WebviewGpuPolicy:    linux.WebviewGpuPolicyAlways,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
