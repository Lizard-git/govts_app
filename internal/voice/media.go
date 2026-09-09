package voice

import (
	"net"

	"example.com/go-voice-mvp/internal/protocol"
)

func RouteVoicePacket(
	conn *net.UDPConn,
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
	conn *net.UDPConn,
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
