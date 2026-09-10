package voice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

func HandlePacket(
	conn *udp.ServerPacketConn,
	hub *Hub,
	cache *RequestCache,
	packet protocol.VoicePacket,
	addr *net.UDPAddr,
) error {
	switch packet.Type {
	case protocol.PacketVoice:
		return HandleVoicePacket(conn, hub, packet, addr)
	case protocol.PacketHello:
		return HandleHelloPacket(conn, hub, cache, packet, addr)
	case protocol.PacketHeartbeat:
		return HandleHeartbeatPacket(hub, packet, addr)
	case protocol.PacketDisconnect:
		return HandleDisconnectPacket(hub, cache, packet, addr)
	case protocol.PacketJoinChannel:
		return HandleJoinChannelPacket(conn, hub, cache, packet, addr)
	default:
		return fmt.Errorf("invalid packet type: %d", packet.Type)
	}
}

func ServeUDP(
	ctx context.Context,
	conn *udp.ServerPacketConn,
	hub *Hub,
	cache *RequestCache,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	stopWakeup := context.AfterFunc(ctx, func() {
		_ = conn.SetReadDeadline(time.Now())
	})
	defer stopWakeup()

	for {
		packet, addr, err := conn.ReadPacket()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("read UDP packet: %w", err)
			}
			if isMalformedPacket(err) {
				continue
			}
			return fmt.Errorf("read UDP packet: %w", err)
		}

		if err := HandlePacket(conn, hub, cache, packet, addr); err != nil {
			log.Printf("cannot handle packet: %v", err)
			continue
		}
	}
}

func isMalformedPacket(err error) bool {
	return errors.Is(err, protocol.ErrRejectedDatagram)
}
