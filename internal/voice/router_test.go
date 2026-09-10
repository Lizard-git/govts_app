package voice

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"example.com/go-voice-mvp/internal/domain"
	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

func TestValidateSessionAddr(t *testing.T) {
	hub := NewHub()

	originalAddr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 5000,
	}

	session := mustCreateSession(t, hub, "alice", originalAddr)

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
	sender := mustCreateSession(t, hub, "alice", nil)

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
	sender := mustCreateSession(t, hub, "alice", nil)
	sameChannel := mustCreateSession(t, hub, "bob", nil)
	otherChannel := mustCreateSession(t, hub, "carol", nil)
	music := mustCreateChannel(t, hub, domain.Channel{Name: "music"})
	gaming := mustCreateChannel(t, hub, domain.Channel{Name: "gaming"})

	if err := hub.JoinChannel(sender.ID, music.ID); err != nil {
		t.Fatal(err)
	}
	if err := hub.JoinChannel(sameChannel.ID, music.ID); err != nil {
		t.Fatal(err)
	}
	if err := hub.JoinChannel(otherChannel.ID, gaming.ID); err != nil {
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

type contextRecordingCodec struct {
	encodedContext protocol.DatagramContext
	encodedPacket  protocol.VoicePacket
}

func (c *contextRecordingCodec) Encode(
	ctx protocol.DatagramContext,
	packet protocol.VoicePacket,
) ([]byte, error) {
	c.encodedContext = ctx
	c.encodedPacket = packet
	return (protocol.PlainDatagramCodec{}).Encode(ctx, packet)
}

func (c *contextRecordingCodec) Decode(
	ctx protocol.DatagramContext,
	datagram []byte,
) (protocol.VoicePacket, error) {
	return (protocol.PlainDatagramCodec{}).Decode(ctx, datagram)
}

func TestSendToSessionUsesRecipientAsKeyOwner(t *testing.T) {
	serverRaw, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	clientRaw, err := net.DialUDP("udp4", nil, serverRaw.LocalAddr().(*net.UDPAddr))
	if err != nil {
		_ = serverRaw.Close()
		t.Fatal(err)
	}

	codec := &contextRecordingCodec{}
	serverConn, err := udp.NewServerPacketConn(serverRaw, codec)
	if err != nil {
		_ = serverRaw.Close()
		_ = clientRaw.Close()
		t.Fatal(err)
	}
	clientConn := mustClientPacketConn(t, clientRaw)
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	const (
		senderID    = 41
		recipientID = 73
	)
	packet := protocol.VoicePacket{
		Type:      protocol.PacketVoice,
		SessionID: senderID,
		Sequence:  9,
		Payload:   []byte("voice"),
	}
	recipient := Session{
		ID:   recipientID,
		Addr: clientConn.LocalAddr().(*net.UDPAddr),
	}
	if err := SendToSession(serverConn, recipient, packet); err != nil {
		t.Fatal(err)
	}

	if codec.encodedContext.Direction != protocol.DirectionServerToClient {
		t.Fatalf("direction = %d, want server to client", codec.encodedContext.Direction)
	}
	if codec.encodedContext.KeyOwnerID != recipientID {
		t.Fatalf(
			"key owner = %d, want recipient %d",
			codec.encodedContext.KeyOwnerID,
			recipientID,
		)
	}
	if codec.encodedPacket.SessionID != senderID {
		t.Fatalf(
			"packet session ID = %d, want sender %d",
			codec.encodedPacket.SessionID,
			senderID,
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
	serverPacketConn := mustPacketConn(t, serverConn)
	defer serverPacketConn.Close()

	clientConn, err := net.DialUDP(
		"udp4",
		nil,
		serverConn.LocalAddr().(*net.UDPAddr),
	)
	if err != nil {
		t.Fatal(err)
	}
	clientPacketConn := mustClientPacketConn(t, clientConn)
	defer clientPacketConn.Close()

	clientAddr := clientPacketConn.LocalAddr().(*net.UDPAddr)
	hub := NewHub()
	music := mustCreateChannel(t, hub, domain.Channel{Name: "music"})
	session := mustCreateSession(t, hub, "alice", clientAddr)
	if err := clientPacketConn.BindSession(session.ID); err != nil {
		t.Fatal(err)
	}
	cache := NewRequestCache()
	request := protocol.VoicePacket{
		Type:      protocol.PacketJoinChannel,
		SessionID: session.ID,
		RequestID: 7,
		Payload:   []byte("music"),
	}

	if err := HandleJoinChannelPacket(
		serverPacketConn,
		hub,
		cache,
		request,
		clientAddr,
	); err != nil {
		t.Fatal(err)
	}
	firstResponse := receiveTestPacket(t, clientPacketConn)

	request.Payload = []byte("gaming")
	if err := HandleJoinChannelPacket(
		serverPacketConn,
		hub,
		cache,
		request,
		clientAddr,
	); err != nil {
		t.Fatal(err)
	}
	secondResponse := receiveTestPacket(t, clientPacketConn)

	updatedSession, ok := hub.Get(session.ID)
	if !ok {
		t.Fatalf("session %d not found", session.ID)
	}
	if got := updatedSession.ChannelID; got != music.ID {
		t.Fatalf("session channel = %d, want original channel %d", got, music.ID)
	}
	if got := string(firstResponse.Payload); got != "music" {
		t.Fatalf("first response channel = %q, want %q", got, "music")
	}
	if got := string(secondResponse.Payload); got != "music" {
		t.Fatalf("cached response channel = %q, want %q", got, "music")
	}
}

func TestHandleJoinChannelPacketRejectsUnknownAndFullChannels(t *testing.T) {
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	serverPacketConn := mustPacketConn(t, serverConn)
	defer serverPacketConn.Close()

	clientConn, err := net.DialUDP("udp4", nil, serverConn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	clientPacketConn := mustClientPacketConn(t, clientConn)
	defer clientPacketConn.Close()

	clientAddr := clientPacketConn.LocalAddr().(*net.UDPAddr)
	hub := NewHub()
	full := mustCreateChannel(t, hub, domain.Channel{Name: "full", MaxUsers: 1})
	occupant := mustCreateSession(t, hub, "occupant", nil)
	if err := hub.JoinChannel(occupant.ID, full.ID); err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, hub, "alice", clientAddr)
	if err := clientPacketConn.BindSession(session.ID); err != nil {
		t.Fatal(err)
	}
	cache := NewRequestCache()

	request := protocol.VoicePacket{
		Type:      protocol.PacketJoinChannel,
		SessionID: session.ID,
		RequestID: 8,
		Payload:   []byte("missing"),
	}
	if err := HandleJoinChannelPacket(serverPacketConn, hub, cache, request, clientAddr); err != nil {
		t.Fatal(err)
	}
	unknownResponse := receiveTestPacket(t, clientPacketConn)
	if unknownResponse.Type != protocol.PacketError ||
		!strings.Contains(string(unknownResponse.Payload), ErrChannelNotFound.Error()) {
		t.Fatalf("unknown channel response = %+v", unknownResponse)
	}
	if len(hub.ListChannels()) != 2 {
		t.Fatal("unknown join created a channel")
	}

	request.RequestID++
	request.Payload = []byte(full.Name)
	if err := HandleJoinChannelPacket(serverPacketConn, hub, cache, request, clientAddr); err != nil {
		t.Fatal(err)
	}
	fullResponse := receiveTestPacket(t, clientPacketConn)
	if fullResponse.Type != protocol.PacketError ||
		!strings.Contains(string(fullResponse.Payload), ErrChannelFull.Error()) {
		t.Fatalf("full channel response = %+v", fullResponse)
	}

	stored, ok := hub.Get(session.ID)
	if !ok {
		t.Fatal("joining session disappeared")
	}
	if stored.ChannelID != 0 {
		t.Fatalf("failed joins moved session to channel %d", stored.ChannelID)
	}
}

func TestHandleHelloPacketReturnsSameSessionForDuplicate(t *testing.T) {
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.ParseIP("127.0.0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	serverPacketConn := mustPacketConn(t, serverConn)
	defer serverPacketConn.Close()

	clientConn, err := net.DialUDP(
		"udp4",
		nil,
		serverConn.LocalAddr().(*net.UDPAddr),
	)
	if err != nil {
		t.Fatal(err)
	}
	clientPacketConn := mustClientPacketConn(t, clientConn)
	defer clientPacketConn.Close()

	clientAddr := clientPacketConn.LocalAddr().(*net.UDPAddr)
	hub := NewHub()
	cache := NewRequestCache()
	hello := protocol.VoicePacket{
		Type:      protocol.PacketHello,
		RequestID: 17,
		Payload:   []byte("alice"),
	}

	if err := HandleHelloPacket(serverPacketConn, hub, cache, hello, clientAddr); err != nil {
		t.Fatal(err)
	}
	firstResponse := receiveTestPacket(t, clientPacketConn)

	if err := HandleHelloPacket(serverPacketConn, hub, cache, hello, clientAddr); err != nil {
		t.Fatal(err)
	}
	secondResponse := receiveTestPacket(t, clientPacketConn)

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

func receiveTestPacket(t *testing.T, conn *udp.ClientPacketConn) protocol.VoicePacket {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	packet, err := conn.ReceivePacket()
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

func mustClientPacketConn(t *testing.T, conn *net.UDPConn) *udp.ClientPacketConn {
	t.Helper()

	packetConn, err := udp.NewClientPacketConn(conn, protocol.PlainDatagramCodec{})
	if err != nil {
		t.Fatalf("NewClientPacketConn() error = %v", err)
	}
	return packetConn
}

func mustPacketConn(t *testing.T, conn *net.UDPConn) *udp.ServerPacketConn {
	t.Helper()

	packetConn, err := udp.NewServerPacketConn(conn, protocol.PlainDatagramCodec{})
	if err != nil {
		t.Fatalf("NewServerPacketConn() error = %v", err)
	}
	return packetConn
}

func TestServeUDPStopsOnContextCancellation(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.ParseIP("127.0.0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	packetConn := mustPacketConn(t, conn)
	defer packetConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	go func() {
		resultCh <- ServeUDP(ctx, packetConn, NewHub(), NewRequestCache())
	}()

	cancel()
	select {
	case err := <-resultCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ServeUDP() error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeUDP() did not stop after context cancellation")
	}
}

func TestServeUDPReturnsClosedConnectionError(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.ParseIP("127.0.0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	packetConn := mustPacketConn(t, conn)
	if err := packetConn.Close(); err != nil {
		t.Fatal(err)
	}

	err = ServeUDP(context.Background(), packetConn, NewHub(), NewRequestCache())
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ServeUDP() error = %v, want %v", err, net.ErrClosed)
	}
}

func TestServeUDPSkipsMalformedPacket(t *testing.T) {
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.ParseIP("127.0.0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	serverPacketConn := mustPacketConn(t, serverConn)
	defer serverPacketConn.Close()

	clientConn, err := net.DialUDP(
		"udp4",
		nil,
		serverConn.LocalAddr().(*net.UDPAddr),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	go func() {
		resultCh <- ServeUDP(ctx, serverPacketConn, NewHub(), NewRequestCache())
	}()

	if _, err := clientConn.Write([]byte{protocol.PacketHello}); err != nil {
		t.Fatal(err)
	}
	clientPacketConn := mustClientPacketConn(t, clientConn)
	defer clientPacketConn.Close()
	if err := clientPacketConn.SendPacket(protocol.VoicePacket{
		Type:      protocol.PacketHello,
		RequestID: 17,
		Payload:   []byte("alice"),
	}); err != nil {
		t.Fatal(err)
	}

	response := receiveTestPacket(t, clientPacketConn)
	if response.Type != protocol.PacketHelloAck {
		t.Fatalf("response type = %d, want %d", response.Type, protocol.PacketHelloAck)
	}

	cancel()
	select {
	case err := <-resultCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ServeUDP() error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeUDP() did not stop")
	}
}
