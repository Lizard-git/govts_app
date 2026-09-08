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

	voiceclient "example.com/go-voice-mvp/internal/client"
	"example.com/go-voice-mvp/internal/transport/udp"
)

func main() {
	name := flag.String("name", "", "client name")
	channel := flag.String("channel", "default", "channel name")
	flag.Parse()

	if err := run(*name, *channel); err != nil {
		log.Fatal(err)
	}
}

func run(name string, channel string) (runErr error) {
	if name == "" {
		return errors.New("client name required")
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := udp.ConnectUDP("127.0.0.1", 9000)
	if err != nil {
		return fmt.Errorf("connect UDP: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close UDP connection: %w", err))
		}
	}()

	sessionID, err := voiceclient.PerformHandshake(signalCtx, conn, name)
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	return runSession(signalCtx, conn, sessionID, name, channel)
}
