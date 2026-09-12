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

	"example.com/go-voice-mvp/internal/audio"
	voiceclient "example.com/go-voice-mvp/internal/client"
	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

const handshakeAttemptTimeout = 3 * time.Second

func main() {
	name := flag.String("name", "", "client name")
	channel := flag.String("channel", "default", "channel ID or name")
	flag.Parse()
	if err := run(*name, *channel); err != nil {
		log.Fatal(err)
	}
}

func run(name, initialChannel string) (runErr error) {
	if name == "" {
		return errors.New("client name required")
	}
	appCtx, cancelApp := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelApp()
	commands := make(chan voiceclient.Command, 16)
	go voiceclient.ReadCommandLoop(appCtx, os.Stdin, commands)
	state := voiceclient.NewState(0, name)
	preference := newChannelPreference(initialChannel)
	backoff := newReconnectBackoff()
	everConnected := false
	var playbackOutput *audio.OtoOutput

	conn, err := openClientPacketConn()
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil && !shouldReplaceClientSocket(err) {
			runErr = errors.Join(runErr, fmt.Errorf("close UDP connection: %w", err))
		}
	}()

	for {
		if err := appCtx.Err(); err != nil {
			state.SetConnectionStatus(voiceclient.ConnectionDisconnected)
			return nil
		}
		if !everConnected {
			state.SetConnectionStatus(voiceclient.ConnectionConnecting)
		} else {
			state.SetConnectionStatus(voiceclient.ConnectionReconnecting)
		}
		sessionID, err := voiceclient.PerformHandshakeAttempt(appCtx, conn, name, handshakeAttemptTimeout)
		if err != nil {
			if appCtx.Err() != nil {
				state.SetConnectionStatus(voiceclient.ConnectionDisconnected)
				return nil
			}
			if shouldReplaceClientSocket(err) {
				replacement, replaceErr := openClientPacketConn()
				if replaceErr != nil {
					log.Printf("cannot replace unusable UDP transport: %v", replaceErr)
				} else {
					_ = conn.Close()
					conn = replacement
				}
			}
			delay := backoff.Next()
			log.Printf("connection unavailable: %v; retry in %s", err, delay)
			if !waitForReconnect(appCtx, delay, commands, cancelApp) {
				return nil
			}
			continue
		}
		if err := conn.BindSession(sessionID); err != nil {
			return fmt.Errorf("bind UDP session: %w", err)
		}
		if _, err := state.StartSession(sessionID); err != nil {
			return fmt.Errorf("start client session: %w", err)
		}
		if playbackOutput == nil {
			playbackOutput, err = audio.NewOtoOutput(clientAudioConfig())
			if err != nil {
				return fmt.Errorf("create audio output: %w", err)
			}
		}
		sessionErr := runSession(appCtx, conn, state, playbackOutput, name, preference, commands, os.Stdout, cancelApp, !everConnected, func() { backoff.Reset(); everConnected = true })
		if appCtx.Err() != nil {
			state.SetConnectionStatus(voiceclient.ConnectionDisconnected)
			return nil
		}
		state.InvalidateSession(voiceclient.ConnectionReconnecting)
		if err := conn.ClearSession(sessionID); err != nil {
			return fmt.Errorf("clear UDP session: %w", err)
		}
		if !errors.Is(sessionErr, voiceclient.ErrConnectionLost) {
			return sessionErr
		}
		if shouldReplaceClientSocket(sessionErr) {
			replacement, replaceErr := openClientPacketConn()
			if replaceErr != nil {
				log.Printf("cannot replace unusable UDP transport: %v", replaceErr)
			} else {
				_ = conn.Close()
				conn = replacement
			}
		}
		delay := backoff.Next()
		log.Printf("%v; retry in %s", sessionErr, delay)
		if !waitForReconnect(appCtx, delay, commands, cancelApp) {
			return nil
		}
	}
}

func openClientPacketConn() (*udp.ClientPacketConn, error) {
	rawConn, err := udp.ConnectUDP("127.0.0.1", 9000)
	if err != nil {
		return nil, fmt.Errorf("connect UDP: %w", err)
	}
	conn, err := udp.NewClientPacketConn(rawConn, protocol.PlainDatagramCodec{})
	if err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("configure UDP packet connection: %w", err)
	}
	return conn, nil
}

func waitForReconnect(ctx context.Context, delay time.Duration, commands <-chan voiceclient.Command, cancel context.CancelFunc) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		case command, ok := <-commands:
			if !ok {
				commands = nil
				continue
			}
			voiceclient.HandleOfflineCommand(command, os.Stdout, cancel)
		}
	}
}
