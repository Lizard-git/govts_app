package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func TestValidPacketType(t *testing.T) {
	if !validPacketType(PacketHello) {
		t.Fatal("PacketHello should be valid")
	}

	if !validPacketType(PacketVoice) {
		t.Fatal("PacketVoice should be valid")
	}

	if validPacketType(100) {
		t.Fatal("packet type 100 should be invalid")
	}
}

func TestEncodeDecodePacket(t *testing.T) {
	original := VoicePacket{
		Type:      PacketVoice,
		SessionID: 42,
		Sequence:  7,
		Payload:   []byte("hello"),
	}

	data := EncodePacket(original)

	decoded, err := decodePacket(data)

	if err != nil {
		t.Fatal(err)
	}

	if decoded.Type != original.Type {
		t.Fatalf(
			"Type: got %d, want %d",
			decoded.Type,
			original.Type,
		)
	}
	if decoded.SessionID != original.SessionID {
		t.Fatalf(
			"SessionID: got %d, want %d",
			decoded.SessionID,
			original.SessionID,
		)
	}
	if decoded.Sequence != original.Sequence {
		t.Fatalf(
			"Sequence: got %d, want %d",
			decoded.Sequence,
			original.Sequence,
		)
	}
	if !bytes.Equal(original.Payload, decoded.Payload) {
		t.Fatalf(
			"Payload: got %v, want %v",
			decoded.Payload,
			original.Payload,
		)
	}
}

func TestDecodePacketTooShort(t *testing.T) {
	data := []byte{1, 2, 3}
	if _, err := decodePacket(data); !errors.Is(err, ErrPacketTooShort) {
		t.Fatalf("expected ErrPacketTooShort, got %v", err)
	}
}

func TestHelloAckRoundTrip(t *testing.T) {
	original := VoicePacket{
		Type:      PacketHelloAck,
		SessionID: 42,
	}

	data := EncodePacket(original)

	decoded, err := decodePacket(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.Type != PacketHelloAck {
		t.Fatalf(
			"expected type %d, got %d",
			PacketHelloAck,
			decoded.Type,
		)
	}

	if decoded.SessionID != 42 {
		t.Fatalf(
			"expected session ID 42, got %d",
			decoded.SessionID,
		)
	}
}
