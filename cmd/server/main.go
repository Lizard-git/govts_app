package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/server"
	"example.com/go-voice-mvp/internal/transport/udp"
	"example.com/go-voice-mvp/internal/voice"
)

func main() {
	configPath := flag.String("config", "", "path to server JSON config")
	flag.Parse()

	if err := run(*configPath); err != nil {
		log.Fatal(err)
	}
}

func run(configPath string) error {
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

	rawConn, err := udp.ListenUDP(9000)
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
		ListenAddress: ":9000",
		ConfigSource:  configSource,
	})
	log.Println("local server console ready; type help")
	go func() {
		if err := console.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("server console disabled: %v", err)
		}
	}()

	log.Printf(
		"voice server listening on :9000 config=%q channels=%d",
		configSource,
		len(hub.ListChannels()),
	)
	serveErr := voice.ServeUDP(ctx, conn, hub, cache)
	cancel()
	cleanupErr := <-cleanupErrCh

	if errors.Is(serveErr, context.Canceled) {
		serveErr = nil
	}
	if errors.Is(cleanupErr, context.Canceled) {
		cleanupErr = nil
	}

	return errors.Join(serveErr, cleanupErr)
}
