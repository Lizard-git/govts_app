package client

import (
	"bufio"
	"context"
	"log"
	"net"
	"os"
	"strings"
)

func CommandLoop(
	ctx context.Context,
	conn *net.UDPConn,
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
	conn *net.UDPConn,
	state *State,
	parts []string,
) {
	if len(parts) != 1 {
		log.Printf("usage: /join <channel>")
		return
	}

	channel := parts[0]
	if err := JoinChannel(ctx, conn, state, channel); err != nil {
		log.Printf("join channel: %v", err)
		return
	}
	log.Printf("join confirmed: %s", channel)
}
