package protocol

import (
	"errors"
	"fmt"
	"net"
)

var ErrSessionNotFound = errors.New("session not found")

type Session struct {
	ID      uint64
	Name    string
	Channel string
	Addr    *net.UDPAddr
}

func SendToSession(conn *net.UDPConn, session *Session, packet VoicePacket) error {
	addr := session.Addr
	if addr == nil {
		return fmt.Errorf("session %d has no UDP address", session.ID)
	}
	return WriteVoicePacket(conn, addr, packet)
}

func SendToSessions(conn *net.UDPConn, sessions []*Session, packet VoicePacket) error {
	for _, session := range sessions {
		if err := SendToSession(conn, session, packet); err != nil {
			return err
		}
	}
	return nil
}

func RouteVoicePacket(conn *net.UDPConn, hub *Hub, packet VoicePacket) error {
	sessions, err := FindRecipients(hub, packet)
	if err != nil {
		return err
	}
	return SendToSessions(conn, sessions, packet)
}

func UpdateSessionAddr(hub *Hub, sessionID uint64, addr *net.UDPAddr) error {
	session, ok := hub.Get(sessionID)
	if !ok {
		return fmt.Errorf("session %d not found", sessionID)
	}
	session.Addr = addr
	return nil
}

func HandleVoicePacket(conn *net.UDPConn, hub *Hub, packet VoicePacket, addr *net.UDPAddr) error {
	if err := UpdateSessionAddr(hub, packet.SessionID, addr); err != nil {
		return err
	}
	return RouteVoicePacket(conn, hub, packet)
}

func HandlePacket(conn *net.UDPConn, hub *Hub, packet VoicePacket, addr *net.UDPAddr) error {
	switch packet.Type {
	case PacketVoice:
		return HandleVoicePacket(conn, hub, packet, addr)
	case PacketHello:
		return UpdateSessionAddr(hub, packet.SessionID, addr)
	default:
		return fmt.Errorf("invalid packet type: %d", packet.Type)
	}
}

func (s *Session) JoinChannel(name string) {
	s.Channel = name
}

type Hub struct {
	sessions map[uint64]*Session
}

func NewHub() *Hub {
	return &Hub{
		sessions: make(map[uint64]*Session),
	}
}

func (h *Hub) Add(s *Session) {
	h.sessions[s.ID] = s
}

func (h *Hub) Remove(id uint64) {
	delete(h.sessions, id)
}

func (h *Hub) Get(id uint64) (*Session, bool) {
	s, ok := h.sessions[id]
	return s, ok
}

func (h *Hub) JoinChannel(id uint64, channel string) error {
	session, ok := h.Get(id)
	if !ok {
		return ErrSessionNotFound
	}
	session.JoinChannel(channel)
	return nil
}

func (h *Hub) Rename(id uint64, name string) error {
	session, ok := h.Get(id)
	if !ok {
		return ErrSessionNotFound
	}
	session.Name = name
	return nil
}

func (h *Hub) Count() int {
	return len(h.sessions)
}

func (h *Hub) Members(channel string) []string {
	members := make([]string, 0)
	for _, session := range h.sessions {
		if session.Channel == channel {
			members = append(members, session.Name)
		}
	}
	return members
}

func (h *Hub) SessionsInChannel(channel string) []*Session {
	sessions := make([]*Session, 0)
	for _, session := range h.sessions {
		if session.Channel == channel {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

func (h *Hub) Recipients(channel string, senderID uint64) []*Session {
	sender, ok := h.Get(senderID)
	if !ok || sender.Channel != channel || sender.Addr == nil {
		return nil
	}

	return []*Session{sender}
}

/*func (h *Hub) Recipients(channel string, senderID uint64) []*Session {
	sessions := make([]*Session, 0)
	for _, session := range h.SessionsInChannel(channel) {
		if session.ID == senderID {
			// continue
		}
		sessions = append(sessions, session)
	}
	return sessions
}*/

func FindSender(hub *Hub, packet VoicePacket) (*Session, error) {
	id := packet.SessionID
	session, ok := hub.Get(id)
	if !ok {
		return nil, fmt.Errorf("session %d not found", packet.SessionID)
	}
	return session, nil
}

func FindRecipients(hub *Hub, packet VoicePacket) ([]*Session, error) {
	senderSession, err := FindSender(hub, packet)
	if err != nil {
		return nil, err
	}
	return hub.Recipients(senderSession.Channel, senderSession.ID), nil
}
