package client

import (
	"bufio"
	"context"
	"log"
	"os"
	"strings"

	"example.com/go-voice-mvp/internal/transport/udp"
)

func CommandLoop(
	ctx context.Context,
	conn *udp.ClientPacketConn,
	state *State,
	cancel context.CancelFunc,
) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 0 {
			continue
		}

		switch parts[0] {
		case "/join":
			handleJoin(ctx, conn, state, parts[1:])
		case "/quit":
			cancel()
			return
		default:
			log.Printf("unknown command: %s", parts[0])
		}
	}
}

func handleJoin(
	ctx context.Context,
	conn *udp.ClientPacketConn,
	state *State,
	parts []string,
) {
	if len(parts) != 1 {
		log.Printf("usage: /join <channel>")
		return
	}

	selector := parts[0]
	channelID, err := ResolveChannel(state.Snapshot(), selector)
	if err != nil {
		log.Printf("join channel: %v", err)
		return
	}
	if err := JoinChannel(ctx, conn, state, channelID); err != nil {
		log.Printf("join channel: %v", err)
		return
	}
	if _, err := LoadServerSnapshot(ctx, conn, state); err != nil {
		log.Printf("refresh server state after join: %v", err)
		return
	}
	log.Printf("join confirmed: %s (id=%d)", selector, channelID)
}
