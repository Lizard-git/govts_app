package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrPacketTooShort = errors.New("packet too short")

const (
	PacketHello    uint8 = 1
	PacketVoice    uint8 = 2
	PacketHelloAck uint8 = 3
)

type VoicePacket struct {
	Type      uint8
	SessionID uint64
	Sequence  uint32
	Payload   []byte
}

func NewVoicePacket(sessionID uint64, sequence uint32, payload []byte) VoicePacket {
	return VoicePacket{
		Type:      PacketVoice,
		SessionID: sessionID,
		Sequence:  sequence,
		Payload:   payload,
	}
}

func packetName(packetType uint8) string {
	switch packetType {
	case PacketHello:
		return "hello"
	case PacketVoice:
		return "voice"
	default:
		return "unknown"
	}
}

func makePayload(text string) []byte {
	return []byte(text)
}

func encodeSessionID(id uint64) []byte {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, id)
	return data
}

// index
// 0    1 2 3 4 5 6 7 8    9 10 11 12
// ┌───┬───────────────────┬─────────────┐
// │ T │     SessionID     │  Sequence   │
// └───┴───────────────────┴─────────────┘
// 1           8                 4

const HeaderSize = 13

func encodeHeader(packet VoicePacket) []byte {
	header := make([]byte, HeaderSize)
	header[0] = packet.Type
	binary.BigEndian.PutUint64(header[1:9], packet.SessionID)
	binary.BigEndian.PutUint32(header[9:HeaderSize], packet.Sequence)
	return header
}

func EncodePacket(packet VoicePacket) []byte {
	header := encodeHeader(packet)
	headerLen := len(header)
	data := make([]byte, headerLen+len(packet.Payload))
	copy(data, header)
	copy(data[headerLen:], packet.Payload)
	return data
}

func DecodePacket(data []byte) (VoicePacket, error) {
	return decodePacket(data)
}

func decodePacket(data []byte) (VoicePacket, error) {
	if len(data) < HeaderSize {
		return VoicePacket{}, ErrPacketTooShort
	}
	packetType := data[0]
	if !validPacketType(packetType) {
		return VoicePacket{}, fmt.Errorf(
			"invalid packet type: %d",
			packetType,
		)
	}
	packet := VoicePacket{
		Type:      packetType,
		SessionID: binary.BigEndian.Uint64(data[1:9]),
		Sequence:  binary.BigEndian.Uint32(data[9:HeaderSize]),
		Payload:   data[HeaderSize:],
	}
	return packet, nil
}

func validPacketType(packetType uint8) bool {
	switch packetType {
	case PacketHello, PacketVoice, PacketHelloAck:
		return true
	default:
		return false
	}
}
