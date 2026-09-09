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

func SendToSession(conn *net.UDPConn, session Session, packet protocol.VoicePacket) error {
	addr := session.Addr
	if addr == nil {
		return fmt.Errorf("session %d has no UDP address", session.ID)
	}
	return udp.WriteVoicePacket(conn, addr, packet)
}

func SendToSessions(conn *net.UDPConn, sessions []Session, packet protocol.VoicePacket) error {
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
	return hub.UpdateAddr(sessionID, addr)
}

func HandleHelloPacket(
	conn *net.UDPConn,
	hub *Hub,
	cache *RequestCache,
	packet protocol.VoicePacket,
	addr *net.UDPAddr,
) error {
	if addr == nil {
		return fmt.Errorf("client UDP address is required")
	}
	if packet.RequestID == 0 {
		return SendError(conn, addr, 0, 0, "handshake request ID is required")
	}

	endpoint := addr.AddrPort()
	if response, ok := cache.GetHandshake(endpoint, packet.RequestID); ok {
		return udp.WriteVoicePacket(conn, addr, response)
	}

	if len(packet.Payload) == 0 {
		return cacheAndSendHandshakeError(
			conn,
			cache,
			packet.RequestID,
			addr,
			"client name is required",
		)
	}
	if len(packet.Payload) > 64 {
		return cacheAndSendHandshakeError(
			conn,
			cache,
			packet.RequestID,
			addr,
			fmt.Sprintf("client name too long: %d bytes", len(packet.Payload)),
		)
	}

	name := string(packet.Payload)
	session := hub.CreateSession(name, addr)

	ack := protocol.VoicePacket{
		Type:      protocol.PacketHelloAck,
		SessionID: session.ID,
		RequestID: packet.RequestID,
	}
	cache.PutHandshake(endpoint, packet.RequestID, ack)
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

func cacheAndSendHandshakeError(
	conn *net.UDPConn,
	cache *RequestCache,
	requestID uint32,
	addr *net.UDPAddr,
	message string,
) error {
	response := protocol.NewErrorPacket(0, requestID, message)
	cache.PutHandshake(addr.AddrPort(), requestID, response)
	return udp.WriteVoicePacket(conn, addr, response)
}

func HandleVoicePacket(
	conn *net.UDPConn,
	hub *Hub,
	packet protocol.VoicePacket,
	addr *net.UDPAddr) error {
	if err := ValidateSessionAddr(hub, packet.SessionID, addr); err != nil {
		return err
	}
	if err := hub.Touch(packet.SessionID); err != nil {
		return err
	}
	return RouteVoicePacket(conn, hub, packet)
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

func HandleHeartbeatPacket(
	hub *Hub,
	packet protocol.VoicePacket,
	addr *net.UDPAddr,
) error {
	if err := ValidateSessionAddr(
		hub,
		packet.SessionID,
		addr,
	); err != nil {
		return err
	}

	return hub.Touch(packet.SessionID)
}

func HandleDisconnectPacket(
	hub *Hub,
	cache *RequestCache,
	packet protocol.VoicePacket,
	addr *net.UDPAddr,
) error {
	if err := ValidateSessionAddr(hub, packet.SessionID, addr); err != nil {
		return err
	}
	session, ok := hub.Get(packet.SessionID)
	if !ok {
		return fmt.Errorf("session %d not found", packet.SessionID)
	}
	removed, ok := hub.Remove(session.ID)
	if !ok {
		return fmt.Errorf("session %d not found", packet.SessionID)
	}
	cache.RemoveSession(session.ID)
	log.Printf("client disconnected: id=%d, name=%s", packet.SessionID, removed.Name)
	return nil
}

func HandleJoinChannelPacket(
	conn *net.UDPConn,
	hub *Hub,
	cache *RequestCache,
	packet protocol.VoicePacket,
	addr *net.UDPAddr,
) error {
	if err := ValidateSessionAddr(hub, packet.SessionID, addr); err != nil {
		return err
	}
	if response, ok := cache.Get(
		packet.SessionID,
		packet.RequestID,
	); ok {
		return udp.WriteVoicePacket(
			conn,
			addr,
			response,
		)
	}

	if len(packet.Payload) == 0 {
		response := protocol.NewErrorPacket(
			packet.SessionID,
			packet.RequestID,
			"channel name is required",
		)
		cache.Put(
			packet.SessionID,
			packet.RequestID,
			response,
		)
		return udp.WriteVoicePacket(
			conn,
			addr,
			response,
		)
	}
	if len(packet.Payload) > 64 {
		response := protocol.NewErrorPacket(
			packet.SessionID,
			packet.RequestID,
			"channel name too long",
		)
		cache.Put(
			packet.SessionID,
			packet.RequestID,
			response,
		)
		return udp.WriteVoicePacket(
			conn,
			addr,
			response,
		)
	}
	channel := string(packet.Payload)
	if err := hub.JoinChannel(packet.SessionID, channel); err != nil {
		return err
	}
	session, ok := hub.Get(packet.SessionID)
	if !ok {
		return fmt.Errorf("session %d not found", packet.SessionID)
	}
	ack := protocol.VoicePacket{
		Type:      protocol.PacketJoinChannelAck,
		SessionID: packet.SessionID,
		RequestID: packet.RequestID,
		Payload:   []byte(channel),
	}
	cache.Put(
		packet.SessionID,
		packet.RequestID,
		ack,
	)
	return SendToSession(conn, session, ack)
}

func HandlePacket(conn *net.UDPConn, hub *Hub, cache *RequestCache, packet protocol.VoicePacket, addr *net.UDPAddr) error {
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

func FindRecipients(hub *Hub, packet protocol.VoicePacket) ([]Session, error) {
	return hub.RecipientsFor(packet.SessionID)
}

func ServeUDP(
	ctx context.Context,
	conn *net.UDPConn,
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
		packet, addr, err := udp.ReadVoicePacket(conn)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("read UDP packet: %w", err)
			}
			if isMalformedPacket(err) {
				log.Printf("bad packet: %v", err)
				continue
			}
			return fmt.Errorf("read UDP packet: %w", err)
		}
		//log.Printf(
		//	"recv type=%d session=%d sequence=%d bytes=%d",
		//	packet.Type,
		//	packet.SessionID,
		//	packet.Sequence,
		//	len(packet.Payload),
		//)
		if err := HandlePacket(conn, hub, cache, packet, addr); err != nil {
			log.Printf("cannot handle packet: %v", err)
			continue
		}
	}
}

func isMalformedPacket(err error) bool {
	return errors.Is(err, protocol.ErrPacketTooShort) ||
		errors.Is(err, protocol.ErrPacketTooLarge) ||
		errors.Is(err, protocol.ErrInvalidPacketType)
}

func SendError(
	conn *net.UDPConn,
	addr *net.UDPAddr,
	sessionID uint64,
	requestID uint32,
	message string,
) error {
	packet := protocol.VoicePacket{
		Type:      protocol.PacketError,
		SessionID: sessionID,
		RequestID: requestID,
		Payload:   []byte(message),
	}
	return udp.WriteVoicePacket(conn, addr, packet)
}
