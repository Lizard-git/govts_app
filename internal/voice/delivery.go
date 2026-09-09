package voice

import (
	"errors"
	"fmt"
	"net"

	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

func SendToSession(
	conn *net.UDPConn,
	session Session,
	packet protocol.VoicePacket,
) error {
	addr := session.Addr
	if addr == nil {
		return fmt.Errorf("session %d has no UDP address", session.ID)
	}
	return udp.WriteVoicePacket(conn, addr, packet)
}

func SendToSessions(
	conn *net.UDPConn,
	sessions []Session,
	packet protocol.VoicePacket,
) error {
	for _, session := range sessions {
		if err := SendToSession(conn, session, packet); err != nil {
			return err
		}
	}
	return nil
}

func UpdateSessionAddr(hub *Hub, sessionID uint64, addr *net.UDPAddr) error {
	return hub.UpdateAddr(sessionID, addr)
}

func ValidateSessionAddr(hub *Hub, sessionID uint64, addr *net.UDPAddr) error {
	if addr == nil {
		return errors.New("client UDP address is required")
	}

	session, ok := hub.Get(sessionID)
	if !ok {
		return fmt.Errorf("session %d not found", sessionID)
	}
	if session.Addr == nil ||
		!session.Addr.IP.Equal(addr.IP) ||
		session.Addr.Port != addr.Port {
		return fmt.Errorf("invalid session address: ip=%s port=%d", addr.IP, addr.Port)
	}
	return nil
}
