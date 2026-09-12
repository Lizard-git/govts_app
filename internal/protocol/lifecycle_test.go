package protocol

import "testing"

func TestValidateEmptyLifecyclePayload(t *testing.T) {
	for _, packetType := range []uint8{PacketHeartbeat, PacketHeartbeatAck, PacketSessionInvalid} {
		if err := ValidateEmptyLifecyclePayload(packetType, nil); err != nil {
			t.Fatalf("type %d: %v", packetType, err)
		}
		if err := ValidateEmptyLifecyclePayload(packetType, []byte{1}); err == nil {
			t.Fatalf("type %d accepted payload", packetType)
		}
	}
	if err := ValidateEmptyLifecyclePayload(PacketVoice, nil); err == nil {
		t.Fatal("voice accepted as lifecycle packet")
	}
}
