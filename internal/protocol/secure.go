package protocol

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

const secureRecordMarker = 0xff
const secureRecordHeader = 17 // marker, session ID, packet counter

type secureSession struct {
	send       cipher.AEAD
	receive    cipher.AEAD
	sendCount  uint64
	receiveMax uint64
	receiveMap uint64
}

// SecureDatagramCodec accepts only handshake packets in plaintext. All
// session packets are AES-GCM protected and checked against a replay window.
type SecureDatagramCodec struct {
	mu       sync.Mutex
	server   bool
	sessions map[uint64]*secureSession
}

func NewSecureDatagramCodec(server bool) *SecureDatagramCodec {
	return &SecureDatagramCodec{server: server, sessions: make(map[uint64]*secureSession)}
}

func (c *SecureDatagramCodec) Install(id uint64, clientToServer, serverToClient [32]byte) error {
	if id == 0 {
		return errors.New("zero secure session ID")
	}
	client, err := newAEAD(clientToServer)
	if err != nil {
		return err
	}
	server, err := newAEAD(serverToClient)
	if err != nil {
		return err
	}
	if c.server {
		client, server = server, client
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[id] = &secureSession{send: client, receive: server}
	return nil
}

func newAEAD(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (c *SecureDatagramCodec) Remove(id uint64) {
	c.mu.Lock()
	delete(c.sessions, id)
	c.mu.Unlock()
}

func (c *SecureDatagramCodec) SessionIDs() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]uint64, 0, len(c.sessions))
	for id := range c.sessions {
		ids = append(ids, id)
	}
	return ids
}

func isAuthPacket(packetType uint8) bool {
	return packetType == PacketAuthInit || packetType == PacketAuthChallenge || packetType == PacketAuthFinish || packetType == PacketAuthAck || packetType == PacketServerVersionTooOld
}

func (c *SecureDatagramCodec) Encode(ctx DatagramContext, packet VoicePacket) ([]byte, error) {
	if isAuthPacket(packet.Type) || (packet.Type == PacketError && packet.SessionID == 0) {
		return EncodePacket(packet)
	}
	ownerID := ctx.KeyOwnerID
	if ownerID == 0 {
		return nil, errors.New("secure packet requires session")
	}
	plain, err := EncodePacket(packet)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	session := c.sessions[ownerID]
	if session == nil {
		return nil, errors.New("secure session not installed")
	}
	session.sendCount++
	if session.sendCount == 0 {
		return nil, errors.New("secure packet counter exhausted")
	}
	header := make([]byte, secureRecordHeader)
	header[0] = secureRecordMarker
	binary.BigEndian.PutUint64(header[1:9], ownerID)
	binary.BigEndian.PutUint64(header[9:17], session.sendCount)
	var nonce [12]byte
	binary.BigEndian.PutUint64(nonce[4:], session.sendCount)
	return session.send.Seal(header, nonce[:], plain, header), nil
}

func (c *SecureDatagramCodec) Decode(ctx DatagramContext, datagram []byte) (VoicePacket, error) {
	if len(datagram) == 0 {
		return VoicePacket{}, rejectDatagram(ErrPacketTooShort)
	}
	if datagram[0] != secureRecordMarker {
		packet, err := DecodePacket(datagram)
		if err != nil {
			return VoicePacket{}, err
		}
		if !isAuthPacket(packet.Type) && !(packet.Type == PacketError && packet.SessionID == 0) && !(c.server && packet.Type == PacketHello) {
			return VoicePacket{}, rejectDatagram(errors.New("unprotected session packet"))
		}
		return packet, nil
	}
	if len(datagram) < secureRecordHeader+16 {
		return VoicePacket{}, rejectDatagram(ErrPacketTooShort)
	}
	id := binary.BigEndian.Uint64(datagram[1:9])
	if !c.server && ctx.KeyOwnerID != id {
		return VoicePacket{}, rejectDatagram(errors.New("secure record belongs to another session"))
	}
	counter := binary.BigEndian.Uint64(datagram[9:17])
	if id == 0 || counter == 0 {
		return VoicePacket{}, rejectDatagram(errors.New("invalid secure record header"))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	session := c.sessions[id]
	if session == nil || !session.canReceive(counter) {
		return VoicePacket{}, rejectDatagram(errors.New("unknown or replayed secure record"))
	}
	var nonce [12]byte
	binary.BigEndian.PutUint64(nonce[4:], counter)
	plain, err := session.receive.Open(nil, nonce[:], datagram[secureRecordHeader:], datagram[:secureRecordHeader])
	if err != nil {
		return VoicePacket{}, rejectDatagram(fmt.Errorf("secure record authentication: %w", err))
	}
	packet, err := DecodePacket(plain)
	if err != nil || isAuthPacket(packet.Type) || (c.server && packet.SessionID != id) || (!c.server && packet.Type != PacketVoice && packet.SessionID != id) {
		return VoicePacket{}, rejectDatagram(errors.New("invalid secure packet"))
	}
	session.markReceived(counter)
	return packet, nil
}

func (s *secureSession) canReceive(counter uint64) bool {
	if counter > s.receiveMax {
		return true
	}
	delta := s.receiveMax - counter
	return delta < 64 && s.receiveMap&(uint64(1)<<delta) == 0
}

func (s *secureSession) markReceived(counter uint64) {
	if counter > s.receiveMax {
		shift := counter - s.receiveMax
		if shift >= 64 {
			s.receiveMap = 1
		} else {
			s.receiveMap = s.receiveMap<<shift | 1
		}
		s.receiveMax = counter
		return
	}
	s.receiveMap |= uint64(1) << (s.receiveMax - counter)
}

var _ DatagramCodec = (*SecureDatagramCodec)(nil)
