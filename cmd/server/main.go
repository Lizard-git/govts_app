package main

import (
	"context"
	"errors"
	"log"

	"example.com/go-voice-mvp/internal/server"
	"example.com/go-voice-mvp/internal/transport/udp"
	"example.com/go-voice-mvp/internal/voice"
)

func main() {
	conn, err := udp.ListenUDP(9000)
	if err != nil {
		log.Fatalf("listen UDP: %v", err)
	}
	defer conn.Close()
	log.Println("voice server listening on :9000")

	hub := voice.NewHub()
	cache := voice.NewRequestCache()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := server.CleanupLoop(
			ctx,
			hub,
			cache,
			server.SessionTimeout,
			server.CleanupInterval,
		); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("cleanup loop stopped: %v", err)
		}
	}()

	if err := voice.ServeUDP(conn, hub, cache); err != nil {
		log.Fatalf("serve UDP: %v", err)
	}
}
