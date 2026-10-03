package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"uniclog.io/govts/internal/clientapp"
	"uniclog.io/govts/internal/clientupdate"
	"uniclog.io/govts/internal/logging"
	wailsui "uniclog.io/govts/internal/ui/wails"
)

func init() {
	application.RegisterEvent[bool]("client-state-changed")
	application.RegisterEvent[bool]("client-event-log-changed")
	application.RegisterEvent[wailsui.AudioMeterDTO]("audio-meter")
}

func main() {
	clientupdate.HandleRecoveryMode()
	updater.HandleHelperMode()
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
	updates := clientupdate.New(filepath.Join(configDir, "Govts"), func() bool { return string(client.Snapshot().ConnectionStatus) == "connected" })
	if err := wailsui.EnableDefaultSettings(service); err != nil {
		log.Printf("load settings: %v", err)
	}
	var mainWindow *application.WebviewWindow
	var quitting atomic.Bool
	app := application.New(application.Options{
		Name:        "Govts",
		Description: "Голосовой клиент Govts",
		Icon:        applicationIcon,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "app.govts.desktop",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if mainWindow != nil {
					showMainWindow(mainWindow)
				}
			},
		},
		Services: []application.Service{
			application.NewService(service),
			application.NewService(updates),
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
	clientupdate.RecordRecoveryStartup(applicationVersion())
	if err := clientupdate.Initialize(updates, app, applicationVersion(), rawUpdatePublicKey); err != nil {
		log.Printf("initialize updater: %v", err)
	}

	bridgeCtx, cancelBridge := context.WithCancel(context.Background())
	stopBridge := wailsui.StartEventBridge(bridgeCtx, app, client)
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			clientupdate.Stop(updates)
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

	mainWindow = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            fmt.Sprintf("Govts %s", applicationVersion()),
		Width:            1180,
		Height:           760,
		MinWidth:         900,
		MinHeight:        620,
		BackgroundColour: application.NewRGB(28, 37, 57),
		URL:              "/",
	})
	// Cancelling the close keeps the process alive. The default listener otherwise
	// destroys the window and quits the client.
	mainWindow.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		if service.CloseToTray() {
			event.Cancel()
			mainWindow.Hide()
			return
		}
		quitting.Store(true)
		app.Quit()
	})
	mainWindow.OnWindowEvent(events.Common.WindowMinimise, func(*application.WindowEvent) {
		if !quitting.Load() {
			mainWindow.Hide()
		}
	})
	installTray(app, mainWindow, &quitting)

	if err := app.Run(); err != nil {
		shutdown()
		log.Printf("client stopped with error: %v", err)
		stopLogging()
		os.Exit(1)
	}
	shutdown()
}

func showMainWindow(window *application.WebviewWindow) {
	window.UnMinimise()
	window.Show()
	window.Focus()
}

func installTray(app *application.App, window *application.WebviewWindow, quitting *atomic.Bool) {
	menu := app.NewMenu()
	menu.Add("Открыть").OnClick(func(*application.Context) {
		showMainWindow(window)
	})
	menu.AddSeparator()
	menu.Add("Выход").OnClick(func(*application.Context) {
		quitting.Store(true)
		app.Quit()
	})

	tray := app.SystemTray.New()
	tray.SetIcon(applicationIcon)
	tray.SetTooltip("Govts")
	tray.SetMenu(menu)
	tray.OnClick(func() { showMainWindow(window) })
}
