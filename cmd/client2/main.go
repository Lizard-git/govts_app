package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	voiceclient "example.com/go-voice-mvp/internal/client"
	"example.com/go-voice-mvp/internal/clientapp"
)

func main() {
	name := flag.String("name", "", "client name")
	channel := flag.String("channel", "default", "channel ID or name")
	server := flag.String("server", "127.0.0.1:9000", "server IP or IP:port (default port 9000)")
	flag.Parse()
	if err := run(*name, *channel, *server); err != nil {
		log.Fatal(err)
	}
}

func run(name, initialChannel, server string) error {
	appCtx, cancelApp := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelApp()
	commands := make(chan voiceclient.Command, 16)
	go voiceclient.ReadCommandLoop(appCtx, os.Stdin, commands)
	app := clientapp.New(clientapp.Options{Commands: commands, Output: os.Stdout, Logger: log.Default()})
	if err := app.Connect(clientapp.ConnectOptions{Name: name, Server: server, InitialChannel: initialChannel}); err != nil {
		return err
	}
	err := app.Wait(appCtx)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	shutdownErr := app.Close(shutdownCtx)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return errors.Join(err, shutdownErr)
}
