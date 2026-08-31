//go:build guispike

// spike-tray wraps the same boot chain in a Wails v3 tray-resident shell:
// tray menu (Open Tomte · Quit), an optional window pointing the embedded
// webview at the server's loopback URL, closing the window hides it rather
// than quitting, and quit tears down server then Postgres.
//
// Build (needs libwebkit2gtk-4.1-dev + libgtk-3-dev on Linux):
//
//	go build -tags guispike ./cmd/spike-tray
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"

	"github.com/gambtho/tomte/app/internal/boot"
)

func main() {
	stateDir := filepath.Join(os.Getenv("HOME"), ".local", "share", "tomte-spike")
	serverBin := "tomte"
	if len(os.Args) > 1 {
		serverBin = os.Args[1]
	}

	sup, err := boot.Start(context.Background(), boot.Config{
		StateDir:  stateDir,
		ServerBin: serverBin,
		Logf:      log.Printf,
	})
	if err != nil {
		log.Fatalf("boot: %v", err)
	}
	defer sup.Stop()

	app := application.New(application.Options{
		Name:        "Tomte",
		Description: "Tomte packaging spike",
		Mac: application.MacOptions{
			// Accessory: tray-resident, no Dock icon — the window is optional.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	}

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Tomte",
		Width:  1100,
		Height: 760,
		URL:    sup.BaseURL,
		Hidden: !sup.FirstRun, // first run opens the window; later launches stay in the tray
	})
	// Closing the window leaves Tomte running — the scheduler is the product.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	menu := app.NewMenu()
	menu.Add("Open Tomte").OnClick(func(*application.Context) { window.Show() })
	menu.AddSeparator()
	menu.Add("Quit Tomte").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
