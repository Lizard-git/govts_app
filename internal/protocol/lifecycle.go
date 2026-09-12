package protocol

import "fmt"

func ValidateEmptyLifecyclePayload(packetType uint8, payload []byte) error {
	if packetType != PacketHeartbeat && packetType != PacketHeartbeatAck && packetType != PacketSessionInvalid {
		return fmt.Errorf("packet type %d has no empty lifecycle payload contract", packetType)
	}
	if len(payload) != 0 {
		return fmt.Errorf("packet type %d payload must be empty, got %d bytes", packetType, len(payload))
	}
	return nil
}
