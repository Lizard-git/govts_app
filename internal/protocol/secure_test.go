package protocol

import (
	"errors"
	"testing"
)

func TestSecureDatagramRejectsTamperingAndReplay(t *testing.T) {
	client := NewSecureDatagramCodec(false)
	server := NewSecureDatagramCodec(true)
	var c2s, s2c [32]byte
	c2s[0], s2c[0] = 1, 2
	if err := client.Install(7, c2s, s2c); err != nil {
		t.Fatal(err)
	}
	if err := server.Install(7, c2s, s2c); err != nil {
		t.Fatal(err)
	}
	ctx := DatagramContext{KeyOwnerID: 7}
	packet := VoicePacket{Type: PacketJoinChannel, SessionID: 7, RequestID: 11, Payload: []byte{1, 2, 3}}
	first, err := client.Encode(ctx, packet)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Encode(ctx, packet)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), first...)
	tampered[len(tampered)-1] ^= 1
	if _, err := server.Decode(DatagramContext{}, tampered); !errors.Is(err, ErrRejectedDatagram) {
		t.Fatalf("tampered record error = %v", err)
	}
	if decoded, err := server.Decode(DatagramContext{}, second); err != nil || decoded.SessionID != 7 {
		t.Fatalf("second record = %+v, %v", decoded, err)
	}
	if decoded, err := server.Decode(DatagramContext{}, first); err != nil || decoded.RequestID != 11 {
		t.Fatalf("out-of-order first record = %+v, %v", decoded, err)
	}
	if _, err := server.Decode(DatagramContext{}, first); !errors.Is(err, ErrRejectedDatagram) {
		t.Fatalf("replayed record error = %v", err)
	}
	if _, err := server.Decode(DatagramContext{}, []byte{PacketJoinChannel}); !errors.Is(err, ErrRejectedDatagram) {
		t.Fatalf("plain session packet error = %v", err)
	}
}

func TestSecureVoiceRelayUsesRecipientKey(t *testing.T) {
	server := NewSecureDatagramCodec(true)
	client := NewSecureDatagramCodec(false)
	var c2s, s2c [32]byte
	c2s[0], s2c[0] = 4, 5
	if err := server.Install(9, c2s, s2c); err != nil {
		t.Fatal(err)
	}
	if err := client.Install(9, c2s, s2c); err != nil {
		t.Fatal(err)
	}
	encoded, err := server.Encode(DatagramContext{KeyOwnerID: 9}, VoicePacket{Type: PacketVoice, SessionID: 8, Sequence: 1, Payload: []byte{6}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := client.Decode(DatagramContext{KeyOwnerID: 9}, encoded)
	if err != nil || packet.SessionID != 8 || packet.Type != PacketVoice {
		t.Fatalf("relayed packet = %+v, %v", packet, err)
	}
	if _, err := client.Decode(DatagramContext{KeyOwnerID: 10}, encoded); !errors.Is(err, ErrRejectedDatagram) {
		t.Fatalf("wrong recipient error = %v", err)
	}
}
