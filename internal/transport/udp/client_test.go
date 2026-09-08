package udp

import (
	"errors"
	"net"
	"testing"
	"time"

	"example.com/go-voice-mvp/internal/protocol"
)

func TestReceivePacketRejectsOversizedDatagram(t *testing.T) {
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

	clientAddr := clientConn.LocalAddr().(*net.UDPAddr)
	if _, err := serverConn.WriteToUDP(
		make([]byte, protocol.MaxDatagramSize+100),
		clientAddr,
	); err != nil {
		t.Fatal(err)
	}
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	if _, err := ReceivePacket(clientConn); !errors.Is(err, protocol.ErrPacketTooLarge) {
		t.Fatalf("ReceivePacket() error = %v, want %v", err, protocol.ErrPacketTooLarge)
	}
}
