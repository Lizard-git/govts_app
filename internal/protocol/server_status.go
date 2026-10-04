package protocol

import (
	"encoding/binary"
	"errors"
)

const ServerStatusVersion byte = 1

func IsServerStatusPacket(kind uint8) bool {
	return kind == PacketServerStatusRequest || kind == PacketServerStatusAck
}

// Status is public, sessionless and deliberately smaller than its request.
func NewServerStatusRequest(requestID uint32, nonce [16]byte) VoicePacket {
	payload := make([]byte, 32)
	payload[0] = ServerStatusVersion
	copy(payload[1:17], nonce[:])
	return VoicePacket{Type: PacketServerStatusRequest, RequestID: requestID, Payload: payload}
}

func NewServerStatusAck(requestID uint32, nonce [16]byte, count uint32) VoicePacket {
	payload := make([]byte, 21)
	payload[0] = ServerStatusVersion
	copy(payload[1:17], nonce[:])
	binary.BigEndian.PutUint32(payload[17:], count)
	return VoicePacket{Type: PacketServerStatusAck, RequestID: requestID, Payload: payload}
}

func DecodeServerStatus(packet VoicePacket) (nonce [16]byte, count uint32, err error) {
	invalid := errors.New("invalid public server status packet")
	if packet.SessionID != 0 || packet.Sequence != 0 || packet.RequestID == 0 {
		return nonce, 0, invalid
	}
	switch packet.Type {
	case PacketServerStatusRequest:
		if len(packet.Payload) != 32 {
			return nonce, 0, invalid
		}
		for _, b := range packet.Payload[17:] {
			if b != 0 {
				return nonce, 0, invalid
			}
		}
	case PacketServerStatusAck:
		if len(packet.Payload) != 21 {
			return nonce, 0, invalid
		}
		count = binary.BigEndian.Uint32(packet.Payload[17:])
	default:
		return nonce, 0, invalid
	}
	if packet.Payload[0] != ServerStatusVersion {
		return nonce, 0, invalid
	}
	copy(nonce[:], packet.Payload[1:17])
	return nonce, count, nil
}

func validPublicStatus(ctx DatagramContext, packet VoicePacket, direction PacketDirection) bool {
	if ctx.KeyOwnerID != 0 || ctx.Direction != direction {
		return false
	}
	expected := PacketServerStatusRequest
	if direction == DirectionServerToClient {
		expected = PacketServerStatusAck
	}
	if packet.Type != expected {
		return false
	}
	_, _, err := DecodeServerStatus(packet)
	return err == nil
}
