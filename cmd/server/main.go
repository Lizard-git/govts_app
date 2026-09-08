package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"example.com/go-voice-mvp/internal/server"
	"example.com/go-voice-mvp/internal/transport/udp"
	"example.com/go-voice-mvp/internal/voice"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	conn, err := udp.ListenUDP(9000)
	if err != nil {
		return fmt.Errorf("listen UDP: %w", err)
	}
	defer conn.Close()

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()

	hub := voice.NewHub()
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

	log.Println("voice server listening on :9000")
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
