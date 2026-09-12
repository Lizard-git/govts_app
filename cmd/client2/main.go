package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/netip"
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
	server := flag.String("server", "127.0.0.1:9000", "server IP or IP:port (default port 9000)")
	flag.Parse()
	if err := run(*name, *channel, *server); err != nil {
		log.Fatal(err)
	}
}

func run(name, initialChannel, server string) (runErr error) {
	if name == "" {
		return errors.New("client name required")
	}
	endpoint, err := parseServerEndpoint(server)
	if err != nil {
		return err
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

	conn, err := openClientPacketConn(endpoint)
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
				replacement, replaceErr := openClientPacketConn(endpoint)
				if replaceErr != nil {
					log.Printf("cannot replace unusable UDP transport: %v", replaceErr)
				} else {
					_ = conn.Close()
					conn = replacement
				}
			}
			delay := backoff.Next()
			log.Printf("connection unavailable: %v; retry in %s", err, delay)
			if !waitForReconnect(appCtx, delay, commands, cancelApp, state) {
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
			replacement, replaceErr := openClientPacketConn(endpoint)
			if replaceErr != nil {
				log.Printf("cannot replace unusable UDP transport: %v", replaceErr)
			} else {
				_ = conn.Close()
				conn = replacement
			}
		}
		delay := backoff.Next()
		log.Printf("%v; retry in %s", sessionErr, delay)
		if !waitForReconnect(appCtx, delay, commands, cancelApp, state) {
			return nil
		}
	}
}

func parseServerEndpoint(server string) (netip.AddrPort, error) {
	if ip, err := netip.ParseAddr(server); err == nil {
		return netip.AddrPortFrom(ip, 9000), nil
	}
	endpoint, err := netip.ParseAddrPort(server)
	if err != nil || endpoint.Port() == 0 {
		return netip.AddrPort{}, fmt.Errorf("invalid server address %q: use IP or IP:port with port 1..65535 (IPv6: [IP]:port)", server)
	}
	return endpoint, nil
}

func openClientPacketConn(endpoint netip.AddrPort) (*udp.ClientPacketConn, error) {
	rawConn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(endpoint))
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

func waitForReconnect(ctx context.Context, delay time.Duration, commands <-chan voiceclient.Command, cancel context.CancelFunc, states ...*voiceclient.State) bool {
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
			voiceclient.HandleOfflineCommand(command, os.Stdout, cancel, states...)
		}
	}
}
