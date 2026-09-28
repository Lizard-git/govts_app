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
		RequestID: 42,
		Payload:   []byte("hello"),
	}

	data, err := EncodePacket(original)
	if err != nil {
		t.Fatal(err)
	}

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
	if decoded.RequestID != original.RequestID {
		t.Fatalf(
			"RequestID = %d, want %d",
			decoded.RequestID,
			original.RequestID,
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
	_, err := decodePacket(data)
	if !errors.Is(err, ErrPacketTooShort) {
		t.Fatalf("expected ErrPacketTooShort, got %v", err)
	}
	if !errors.Is(err, ErrRejectedDatagram) {
		t.Fatalf("expected ErrRejectedDatagram, got %v", err)
	}
}

func TestHelloAckRoundTrip(t *testing.T) {
	original := VoicePacket{
		Type:      PacketHelloAck,
		SessionID: 42,
	}

	data, err := EncodePacket(original)
	if err != nil {
		t.Fatal(err)
	}

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

func TestEncodePacketRejectsOversizedPayload(t *testing.T) {
	packet := VoicePacket{
		Type:    PacketVoice,
		Payload: make([]byte, MaxPayloadSize+1),
	}

	if _, err := EncodePacket(packet); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("EncodePacket() error = %v, want %v", err, ErrPayloadTooLarge)
	}
}

func TestDecodePacketRejectsOversizedDatagram(t *testing.T) {
	data := make([]byte, MaxWireDatagramSize+1)
	data[0] = PacketVoice

	if _, err := DecodePacket(data); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("DecodePacket() error = %v, want %v", err, ErrPacketTooLarge)
	}
}

func TestEncodeDecodeMaximumPayload(t *testing.T) {
	original := VoicePacket{
		Type:      PacketVoice,
		SessionID: 42,
		Sequence:  7,
		Payload:   make([]byte, MaxPayloadSize),
	}

	data, err := EncodePacket(original)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != HeaderSize+MaxPayloadSize {
		t.Fatalf("encoded size = %d, want %d", len(data), HeaderSize+MaxPayloadSize)
	}
	if _, err := DecodePacket(data); err != nil {
		t.Fatal(err)
	}
}

func TestEncodePacketRejectsInvalidType(t *testing.T) {
	_, err := EncodePacket(VoicePacket{})
	if !errors.Is(err, ErrInvalidPacketType) {
		t.Fatalf("EncodePacket() error = %v, want %v", err, ErrInvalidPacketType)
	}
	if errors.Is(err, ErrRejectedDatagram) {
		t.Fatalf("local encode error unexpectedly classified as rejected datagram: %v", err)
	}
}

func TestDecodePacketClassifiesInvalidTypeAsRejectedDatagram(t *testing.T) {
	data := make([]byte, HeaderSize)
	data[0] = PacketEnd

	_, err := DecodePacket(data)
	if !errors.Is(err, ErrInvalidPacketType) {
		t.Fatalf("DecodePacket() error = %v, want %v", err, ErrInvalidPacketType)
	}
	if !errors.Is(err, ErrRejectedDatagram) {
		t.Fatalf("DecodePacket() error = %v, want %v", err, ErrRejectedDatagram)
	}
}
