package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	workbench "local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	var desktopContext context.Context
	application, err := workbench.New(
		filepath.Join(configDir, "Point"),
		workbench.WithDirectoryPicker(func(ctx context.Context) (string, error) {
			return runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{Title: "Open a local workspace"})
		}),
		workbench.WithEventSink(func(event domain.Event) {
			if desktopContext != nil {
				runtime.EventsEmit(desktopContext, "workbench:event", event)
			}
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	err = wails.Run(&options.App{Title: "Point", Width: 1440, Height: 900, MinWidth: 1040, MinHeight: 680, AssetServer: &assetserver.Options{Assets: assets}, BackgroundColour: &options.RGBA{R: 9, G: 13, B: 19, A: 1}, OnStartup: func(ctx context.Context) { desktopContext = ctx; application.Startup(ctx) }, OnShutdown: application.Shutdown, Bind: []interface{}{application}})
	if err != nil {
		log.Fatal(err)
	}
}
