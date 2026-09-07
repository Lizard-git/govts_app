package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrPacketTooShort = errors.New("packet too short")

const (
	PacketHello uint8 = iota + 1
	PacketVoice
	PacketHelloAck
	PacketHeartbeat
	PacketDisconnect
	PacketJoinChannel
	PacketJoinChannelAck
	PacketError

	PacketEnd
)

type VoicePacket struct {
	Type      uint8
	SessionID uint64
	Sequence  uint32
	RequestID uint32
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
// 0        Type       1 byte
// 1..8     SessionID  8 bytes
// 9..12    Sequence   4 bytes
// 13..16   RequestID  4 bytes
// 17..N     Payload

const HeaderSize = 17

func encodeHeader(packet VoicePacket) []byte {
	header := make([]byte, HeaderSize)
	header[0] = packet.Type
	binary.BigEndian.PutUint64(header[1:9], packet.SessionID)
	binary.BigEndian.PutUint32(header[9:13], packet.Sequence)
	binary.BigEndian.PutUint32(header[13:17], packet.RequestID)
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
		Sequence:  binary.BigEndian.Uint32(data[9:13]),
		RequestID: binary.BigEndian.Uint32(data[13:17]),
		Payload:   data[17:],
	}
	return packet, nil
}

func validPacketType(packetType uint8) bool {
	return packetType > 0 && packetType < PacketEnd
	/*switch packetType {
	case PacketHello, PacketVoice, PacketHelloAck, PacketHeartbeat,
		PacketDisconnect, PacketJoinChannel, PacketJoinChannelAck:
		return true
	default:
		return false
	}*/
}
