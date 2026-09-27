package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"uniclog.io/govts/internal/appversion"
	"uniclog.io/govts/internal/media"
	"uniclog.io/govts/internal/protocol"
	"uniclog.io/govts/internal/server"
	"uniclog.io/govts/internal/transport/udp"
	"uniclog.io/govts/internal/voice"
)

func main() {
	showVersion := flag.Bool("version", false, "print server version and exit")
	configPath := flag.String("config", "", "path to server JSON config")
	port := flag.Int("port", 9000, "UDP listen port (1..65535)")
	mediaPort := flag.Int("media-port", -1, "HTTPS media signaling port; -1 uses voice port + 2, 0 disables screen sharing")
	mediaMinPort := flag.Int("media-min-port", 20000, "first UDP port used by WebRTC")
	mediaMaxPort := flag.Int("media-max-port", 20100, "last UDP port used by WebRTC")
	mediaAdvertisedIP := flag.String("media-advertised-ip", "", "public IP advertised by WebRTC; empty uses local interfaces")
	mediaIdentity := flag.String("media-identity", "govts-media", "path prefix for generated media TLS certificate and key")
	flag.Parse()
	if *showVersion {
		fmt.Println(serverVersion())
		return
	}

	if err := run(*configPath, *port, *mediaPort, *mediaMinPort, *mediaMaxPort, *mediaAdvertisedIP, *mediaIdentity); err != nil {
		log.Fatal(err)
	}
}

func run(configPath string, port, mediaPort, mediaMinPort, mediaMaxPort int, mediaAdvertisedIP, mediaIdentity string) error {
	currentVersion, err := appversion.Parse(serverVersion())
	if err != nil {
		return fmt.Errorf("invalid embedded server version: %w", err)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid UDP port %d: must be 1..65535", port)
	}
	if mediaPort == -1 {
		mediaPort = port + 2
	}
	startedAt := time.Now()
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()

	var source server.BootstrapSource = server.BuiltinBootstrapSource{}
	configSource := "builtin"
	if configPath != "" {
		source = server.JSONBootstrapSource{Path: configPath}
		configSource = configPath
	}
	hub, err := server.BootstrapHub(ctx, source)
	if err != nil {
		return fmt.Errorf("bootstrap server channels: %w", err)
	}
	hub.SetServerVersion(currentVersion)
	if mediaPort != 0 && (mediaPort < 1 || mediaPort > 65535) {
		return errors.New("invalid media signaling port")
	}
	hub.SetMediaPort(uint16(mediaPort))
	var mediaServer *http.Server
	var mediaManager *media.Manager
	mediaErrCh := make(chan error, 1)
	if mediaPort != 0 {
		if mediaPort < 1 || mediaPort > 65535 || mediaMinPort < 1 || mediaMaxPort > 65535 || mediaMaxPort < mediaMinPort {
			return errors.New("invalid media port configuration")
		}
		identity, identityErr := media.LoadOrCreateIdentity(mediaIdentity+".crt", mediaIdentity+".key")
		if identityErr != nil {
			return fmt.Errorf("media identity: %w", identityErr)
		}
		mediaManager, err = media.NewManagerWithConfig(hub, media.Config{MinUDPPort: uint16(mediaMinPort), MaxUDPPort: uint16(mediaMaxPort), AdvertisedIP: mediaAdvertisedIP})
		if err != nil {
			return fmt.Errorf("create media manager: %w", err)
		}
		handler, handlerErr := media.NewHTTPHandler(hub, mediaManager)
		if handlerErr != nil {
			return handlerErr
		}
		mediaServer = &http.Server{Addr: fmt.Sprintf(":%d", mediaPort), Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second}
		log.Printf("media signaling configured on https://0.0.0.0:%d fingerprint=%s udp=%d-%d advertised_ip=%q", mediaPort, identity.Fingerprint, mediaMinPort, mediaMaxPort, mediaAdvertisedIP)
		if mediaAdvertisedIP == "" {
			log.Printf("media warning: advertised IP is empty; public-IP deployments must set -media-advertised-ip")
		}
		go func() { mediaErrCh <- mediaServer.ListenAndServeTLS(identity.CertPath, identity.KeyPath); cancel() }()
	} else {
		close(mediaErrCh)
	}

	rawConn, err := udp.ListenUDP(port)
	if err != nil {
		return fmt.Errorf("listen UDP: %w", err)
	}
	conn, err := udp.NewServerPacketConn(rawConn, protocol.PlainDatagramCodec{})
	if err != nil {
		_ = rawConn.Close()
		return fmt.Errorf("configure UDP packet connection: %w", err)
	}
	defer conn.Close()

	cache := voice.NewRequestCache()
	cleanupErrCh := make(chan error, 1)
	dispatchErrCh := make(chan error, 1)
	go func() {
		err := voice.DispatchEvents(ctx, hub, conn)
		dispatchErrCh <- err
		cancel()
	}()

	go func() {
		cleanupErrCh <- server.CleanupLoop(
			ctx,
			hub,
			cache,
			server.SessionTimeout,
			server.CleanupInterval,
		)
	}()
	console := server.NewConsole(hub, os.Stdin, os.Stdout, server.ConsoleInfo{
		StartedAt:     startedAt,
		ListenAddress: rawConn.LocalAddr().String(),
		ConfigSource:  configSource,
	})
	log.Println("local server console ready; type help")
	go func() {
		if err := console.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("server console disabled: %v", err)
		}
	}()

	log.Printf(
		"voice server version=%s listening on %s config=%q channels=%d",
		currentVersion,
		rawConn.LocalAddr(),
		configSource,
		len(hub.ListChannels()),
	)
	serveErr := voice.ServeUDP(ctx, conn, hub, cache)
	cancel()
	if mediaServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mediaServer.Shutdown(shutdownCtx)
		shutdownCancel()
		mediaManager.Close()
	}
	cleanupErr := <-cleanupErrCh
	dispatchErr := <-dispatchErrCh
	if errors.Is(dispatchErr, context.Canceled) {
		dispatchErr = nil
	}

	if errors.Is(serveErr, context.Canceled) {
		serveErr = nil
	}
	if errors.Is(cleanupErr, context.Canceled) {
		cleanupErr = nil
	}

	var mediaErr error
	if mediaPort != 0 {
		mediaErr = <-mediaErrCh
		if errors.Is(mediaErr, http.ErrServerClosed) {
			mediaErr = nil
		}
	}
	return errors.Join(serveErr, cleanupErr, dispatchErr, mediaErr)
}
