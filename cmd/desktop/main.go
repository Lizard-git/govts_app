package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"uniclog.io/govts/internal/clientapp"
	"uniclog.io/govts/internal/logging"
	wailsui "uniclog.io/govts/internal/ui/wails"
)

func init() {
	application.RegisterEvent[bool]("client-state-changed")
	application.RegisterEvent[bool]("client-event-log-changed")
	application.RegisterEvent[wailsui.AudioMeterDTO]("audio-meter")
}

func main() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("resolve application data directory: %v", err)
	}
	webviewDataPath := filepath.Join(configDir, "Govts", "WebView2")
	stopLogging, err := logging.Start(filepath.Join(configDir, "Govts", "logs", "client.log"))
	if err != nil {
		log.Printf("file logging unavailable; continuing with console logging: %v", err)
	}
	defer stopLogging()
	log.Printf("client starting: version=%s", applicationVersion())

	client := clientapp.New(clientapp.Options{Logger: log.Default(), Secure: true})
	service := wailsui.NewService(client)
	if err := wailsui.EnableDefaultSettings(service); err != nil {
		log.Printf("load settings: %v", err)
	}
	app := application.New(application.Options{
		Name:        "Govts",
		Description: "Голосовой клиент Govts",
		Services: []application.Service{
			application.NewService(service),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Windows: application.WindowsOptions{
			WebviewUserDataPath: webviewDataPath,
		},
	})

	bridgeCtx, cancelBridge := context.WithCancel(context.Background())
	stopBridge := wailsui.StartEventBridge(bridgeCtx, app, client)
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			cancelBridge()
			stopBridge()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := client.Close(ctx); err != nil {
				log.Printf("close client: %v", err)
			}
		})
	}
	app.OnShutdown(shutdown)

	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            fmt.Sprintf("Govts %s", applicationVersion()),
		Width:            1180,
		Height:           760,
		MinWidth:         900,
		MinHeight:        620,
		BackgroundColour: application.NewRGB(28, 37, 57),
		URL:              "/",
	})
	mainWindow.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		app.Quit()
	})

	if err := app.Run(); err != nil {
		shutdown()
		log.Printf("client stopped with error: %v", err)
		stopLogging()
		os.Exit(1)
	}
	shutdown()
}
