package voice

import (
	"net"

	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

func RouteVoicePacket(
	conn *udp.ServerPacketConn,
	hub *Hub,
	packet protocol.VoicePacket,
) error {
	sessions, err := FindRecipients(hub, packet)
	if err != nil {
		return err
	}
	return SendToSessions(conn, sessions, packet)
}

func HandleVoicePacket(
	conn *udp.ServerPacketConn,
	hub *Hub,
	packet protocol.VoicePacket,
	addr *net.UDPAddr,
) error {
	if err := ValidateSessionAddr(hub, packet.SessionID, addr); err != nil {
		return err
	}
	if err := hub.Touch(packet.SessionID); err != nil {
		return err
	}
	return RouteVoicePacket(conn, hub, packet)
}

func FindRecipients(hub *Hub, packet protocol.VoicePacket) ([]Session, error) {
	return hub.RecipientsFor(packet.SessionID)
}
