package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"example.com/go-voice-mvp/internal/clientapp"
	wailsui "example.com/go-voice-mvp/internal/ui/wails"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	application.RegisterEvent[bool]("client-state-changed")
	application.RegisterEvent[bool]("client-event-log-changed")
	application.RegisterEvent[wailsui.AudioMeterDTO]("audio-meter")
}

func main() {
	client := clientapp.New(clientapp.Options{Logger: log.Default()})
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

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            fmt.Sprintf("Govts %s", applicationVersion()),
		Width:            1180,
		Height:           760,
		MinWidth:         900,
		MinHeight:        620,
		BackgroundColour: application.NewRGB(28, 37, 57),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		shutdown()
		log.Fatal(err)
	}
	shutdown()
}
