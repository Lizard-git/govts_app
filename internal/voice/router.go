package voice

import (
	"fmt"
	"log"
	"net"

	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

func SendToSession(conn *net.UDPConn, session *Session, packet protocol.VoicePacket) error {
	addr := session.Addr
	if addr == nil {
		return fmt.Errorf("session %d has no UDP address", session.ID)
	}
	return udp.WriteVoicePacket(conn, addr, packet)
}

func SendToSessions(conn *net.UDPConn, sessions []*Session, packet protocol.VoicePacket) error {
	for _, session := range sessions {
		if err := SendToSession(conn, session, packet); err != nil {
			return err
		}
	}
	return nil
}

func RouteVoicePacket(conn *net.UDPConn, hub *Hub, packet protocol.VoicePacket) error {
	sessions, err := FindRecipients(hub, packet)
	if err != nil {
		return err
	}
	return SendToSessions(conn, sessions, packet)
}

func UpdateSessionAddr(
	hub *Hub,
	sessionID uint64,
	addr *net.UDPAddr,
) error {
	session, ok := hub.Get(sessionID)
	if !ok {
		return fmt.Errorf("session %d not found", sessionID)
	}

	session.Addr = addr
	return nil
}

func HandleHelloPacket(
	conn *net.UDPConn,
	hub *Hub,
	packet protocol.VoicePacket,
	addr *net.UDPAddr,
) error {
	if len(packet.Payload) == 0 {
		return fmt.Errorf("client name is required")
	}
	if len(packet.Payload) > 64 {
		return fmt.Errorf("client name too long: %d bytes", len(packet.Payload))
	}

	name := string(packet.Payload)
	session := hub.CreateSession(name, addr)

	ack := protocol.VoicePacket{
		Type:      protocol.PacketHelloAck,
		SessionID: session.ID,
	}
	if err := SendToSession(conn, session, ack); err != nil {
		return err
	}

	log.Printf(
		"client connected: id=%d name=%q addr=%s",
		session.ID,
		session.Name,
		session.Addr,
	)

	return nil
}

func HandleVoicePacket(
	conn *net.UDPConn,
	hub *Hub,
	packet protocol.VoicePacket,
	addr *net.UDPAddr) error {
	if err := UpdateSessionAddr(hub, packet.SessionID, addr); err != nil {
		return err
	}
	return RouteVoicePacket(conn, hub, packet)
}

func HandlePacket(conn *net.UDPConn, hub *Hub, packet protocol.VoicePacket, addr *net.UDPAddr) error {
	switch packet.Type {
	case protocol.PacketVoice:
		return HandleVoicePacket(conn, hub, packet, addr)
	case protocol.PacketHello:
		return HandleHelloPacket(conn, hub, packet, addr)
	default:
		return fmt.Errorf("invalid packet type: %d", packet.Type)
	}
}

func FindSender(hub *Hub, packet protocol.VoicePacket) (*Session, error) {
	id := packet.SessionID
	session, ok := hub.Get(id)
	if !ok {
		return nil, fmt.Errorf("session %d not found", packet.SessionID)
	}
	return session, nil
}

func FindRecipients(hub *Hub, packet protocol.VoicePacket) ([]*Session, error) {
	senderSession, err := FindSender(hub, packet)
	if err != nil {
		return nil, err
	}
	return hub.Recipients(senderSession.Channel, senderSession.ID), nil
}

func ServeUDP(conn *net.UDPConn, hub *Hub) error {
	for {
		packet, addr, err := udp.ReadVoicePacket(conn)
		if err != nil {
			log.Printf("bad packet: %v", err)
			continue
		}
		log.Printf(
			"recv type=%d session=%d sequence=%d bytes=%d",
			packet.Type,
			packet.SessionID,
			packet.Sequence,
			len(packet.Payload),
		)
		if err := HandlePacket(conn, hub, packet, addr); err != nil {
			log.Printf("cannot handle packet: %v", err)
			continue
		}
	}
}
