package voice

import (
	"fmt"
	"log"
	"net"

	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

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
	session, err := hub.CreateSession(name, addr)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
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

func HandleHeartbeatPacket(
	hub *Hub,
	packet protocol.VoicePacket,
	addr *net.UDPAddr,
) error {
	if err := ValidateSessionAddr(hub, packet.SessionID, addr); err != nil {
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
	if response, ok := cache.Get(packet.SessionID, packet.RequestID); ok {
		return udp.WriteVoicePacket(conn, addr, response)
	}

	if len(packet.Payload) == 0 {
		response := protocol.NewErrorPacket(
			packet.SessionID,
			packet.RequestID,
			"channel name is required",
		)
		cache.Put(packet.SessionID, packet.RequestID, response)
		return udp.WriteVoicePacket(conn, addr, response)
	}
	if len(packet.Payload) > 64 {
		response := protocol.NewErrorPacket(
			packet.SessionID,
			packet.RequestID,
			"channel name too long",
		)
		cache.Put(packet.SessionID, packet.RequestID, response)
		return udp.WriteVoicePacket(conn, addr, response)
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
	cache.Put(packet.SessionID, packet.RequestID, ack)
	return SendToSession(conn, session, ack)
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
