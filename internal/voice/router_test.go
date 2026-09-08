package voice

import (
	"errors"
	"net"
	"testing"
	"time"

	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

func TestValidateSessionAddr(t *testing.T) {
	hub := NewHub()

	originalAddr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 5000,
	}

	session := hub.CreateSession("alice", originalAddr)

	t.Run("same address", func(t *testing.T) {
		addr := &net.UDPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 5000,
		}

		err := ValidateSessionAddr(hub, session.ID, addr)
		if err != nil {
			t.Fatalf("expected valid address, got %v", err)
		}
	})

	t.Run("different port", func(t *testing.T) {
		addr := &net.UDPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 6000,
		}

		err := ValidateSessionAddr(hub, session.ID, addr)
		if err == nil {
			t.Fatal("expected address mismatch")
		}
	})

	t.Run("unknown session", func(t *testing.T) {
		err := ValidateSessionAddr(
			hub,
			999,
			originalAddr,
		)

		if err == nil {
			t.Fatal("expected unknown session error")
		}
	})
}

func TestFindRecipientsRejectsSenderWithoutChannel(t *testing.T) {
	hub := NewHub()
	sender := hub.CreateSession("alice", nil)

	_, err := FindRecipients(hub, protocol.VoicePacket{
		Type:      protocol.PacketVoice,
		SessionID: sender.ID,
	})
	if !errors.Is(err, ErrSessionNotInChannel) {
		t.Fatalf(
			"FindRecipients() error = %v, want %v",
			err,
			ErrSessionNotInChannel,
		)
	}
}

func TestFindRecipientsReturnsOnlySameChannel(t *testing.T) {
	hub := NewHub()
	sender := hub.CreateSession("alice", nil)
	sameChannel := hub.CreateSession("bob", nil)
	otherChannel := hub.CreateSession("carol", nil)

	if err := hub.JoinChannel(sender.ID, "music"); err != nil {
		t.Fatal(err)
	}
	if err := hub.JoinChannel(sameChannel.ID, "music"); err != nil {
		t.Fatal(err)
	}
	if err := hub.JoinChannel(otherChannel.ID, "gaming"); err != nil {
		t.Fatal(err)
	}

	recipients, err := FindRecipients(hub, protocol.VoicePacket{
		Type:      protocol.PacketVoice,
		SessionID: sender.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 {
		t.Fatalf("FindRecipients() returned %d sessions, want 1", len(recipients))
	}
	if recipients[0].ID != sameChannel.ID {
		t.Fatalf(
			"FindRecipients() returned session %d, want %d",
			recipients[0].ID,
			sameChannel.ID,
		)
	}
}

func TestHandleJoinChannelPacketReturnsCachedResponseForDuplicate(t *testing.T) {
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
	hub := NewHub()
	session := hub.CreateSession("alice", clientAddr)
	cache := NewRequestCache()
	request := protocol.VoicePacket{
		Type:      protocol.PacketJoinChannel,
		SessionID: session.ID,
		RequestID: 7,
		Payload:   []byte("music"),
	}

	if err := HandleJoinChannelPacket(
		serverConn,
		hub,
		cache,
		request,
		clientAddr,
	); err != nil {
		t.Fatal(err)
	}
	firstResponse := receiveTestPacket(t, clientConn)

	request.Payload = []byte("gaming")
	if err := HandleJoinChannelPacket(
		serverConn,
		hub,
		cache,
		request,
		clientAddr,
	); err != nil {
		t.Fatal(err)
	}
	secondResponse := receiveTestPacket(t, clientConn)

	if got := session.Channel; got != "music" {
		t.Fatalf("session channel = %q, want original channel %q", got, "music")
	}
	if got := string(firstResponse.Payload); got != "music" {
		t.Fatalf("first response channel = %q, want %q", got, "music")
	}
	if got := string(secondResponse.Payload); got != "music" {
		t.Fatalf("cached response channel = %q, want %q", got, "music")
	}
}

func TestHandleHelloPacketReturnsSameSessionForDuplicate(t *testing.T) {
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
	hub := NewHub()
	cache := NewRequestCache()
	hello := protocol.VoicePacket{
		Type:      protocol.PacketHello,
		RequestID: 17,
		Payload:   []byte("alice"),
	}

	if err := HandleHelloPacket(serverConn, hub, cache, hello, clientAddr); err != nil {
		t.Fatal(err)
	}
	firstResponse := receiveTestPacket(t, clientConn)

	if err := HandleHelloPacket(serverConn, hub, cache, hello, clientAddr); err != nil {
		t.Fatal(err)
	}
	secondResponse := receiveTestPacket(t, clientConn)

	if hub.Count() != 1 {
		t.Fatalf("Hub.Count() = %d, want 1", hub.Count())
	}
	if firstResponse.SessionID == 0 {
		t.Fatal("first response returned zero session ID")
	}
	if secondResponse.SessionID != firstResponse.SessionID {
		t.Fatalf(
			"duplicate hello returned session %d, want %d",
			secondResponse.SessionID,
			firstResponse.SessionID,
		)
	}
	if secondResponse.RequestID != hello.RequestID {
		t.Fatalf(
			"duplicate hello response RequestID = %d, want %d",
			secondResponse.RequestID,
			hello.RequestID,
		)
	}
}

func receiveTestPacket(t *testing.T, conn *net.UDPConn) protocol.VoicePacket {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	packet, err := udp.ReceivePacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	return packet
}
