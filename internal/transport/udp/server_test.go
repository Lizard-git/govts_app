package udp

import (
	"errors"
	"net"
	"testing"
	"time"

	"example.com/go-voice-mvp/internal/protocol"
)

func TestReadVoicePacketRejectsOversizedDatagram(t *testing.T) {
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.ParseIP("127.0.0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()

	clientConn, err := net.DialUDP(
		"udp4",
		nil,
		serverConn.LocalAddr().(*net.UDPAddr),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write(make([]byte, protocol.MaxDatagramSize+100)); err != nil {
		t.Fatal(err)
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	if _, _, err := ReadVoicePacket(serverConn); !errors.Is(err, protocol.ErrPacketTooLarge) {
		t.Fatalf("ReadVoicePacket() error = %v, want %v", err, protocol.ErrPacketTooLarge)
	}
}
