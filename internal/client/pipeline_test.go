package client

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"example.com/go-voice-mvp/internal/audio"
	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

type joinTestPeer struct {
	serverConn *net.UDPConn
	clientConn *net.UDPConn
	state      *State
	ctx        context.Context
}

func newJoinTestPeer(t *testing.T) *joinTestPeer {
	t.Helper()

	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.ParseIP("127.0.0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}

	clientConn, err := net.DialUDP(
		"udp4",
		nil,
		serverConn.LocalAddr().(*net.UDPAddr),
	)
	if err != nil {
		_ = serverConn.Close()
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	state := NewState(42, "alice", "")
	encodedCh := make(chan audio.Frame, 1)
	controlCh := make(chan protocol.VoicePacket, 4)

	go func() {
		_ = ReceiveLoop(ctx, clientConn, encodedCh, controlCh)
	}()
	go func() {
		_ = ControlLoop(ctx, state, controlCh)
	}()

	t.Cleanup(func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	return &joinTestPeer{
		serverConn: serverConn,
		clientConn: clientConn,
		state:      state,
		ctx:        ctx,
	}
}

func (p *joinTestPeer) receiveRequest() (protocol.VoicePacket, *net.UDPAddr, error) {
	if err := p.serverConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return protocol.VoicePacket{}, nil, err
	}
	return udp.ReadVoicePacket(p.serverConn)
}

func (p *joinTestPeer) sendResponse(
	addr *net.UDPAddr,
	response protocol.VoicePacket,
) error {
	return udp.WriteVoicePacket(p.serverConn, addr, response)
}

func TestJoinChannelRejectsEmptyName(t *testing.T) {
	state := NewState(42, "alice", "")

	err := JoinChannel(context.Background(), nil, state, "")
	if err == nil {
		t.Fatal("JoinChannel() error = nil, want empty channel error")
	}
	if !strings.Contains(err.Error(), "channel name is required") {
		t.Fatalf("JoinChannel() error = %q, want empty channel error", err)
	}
}

func TestPerformHandshakeRetriesWithSameRequestID(t *testing.T) {
	serverConn, clientConn := newHandshakeTestConnections(t)
	serverErrCh := make(chan error, 1)

	go func() {
		if err := serverConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			serverErrCh <- err
			return
		}
		first, _, err := udp.ReadVoicePacket(serverConn)
		if err != nil {
			serverErrCh <- err
			return
		}
		second, addr, err := udp.ReadVoicePacket(serverConn)
		if err != nil {
			serverErrCh <- err
			return
		}
		if second.RequestID != first.RequestID {
			serverErrCh <- fmt.Errorf(
				"retry RequestID = %d, want %d",
				second.RequestID,
				first.RequestID,
			)
			return
		}
		serverErrCh <- udp.WriteVoicePacket(serverConn, addr, protocol.VoicePacket{
			Type:      protocol.PacketHelloAck,
			SessionID: 99,
			RequestID: second.RequestID,
		})
	}()

	sessionID, err := performHandshakeWithRequestID(
		context.Background(),
		clientConn,
		"alice",
		17,
		20*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if serverErr := <-serverErrCh; serverErr != nil {
		t.Fatal(serverErr)
	}
	if sessionID != 99 {
		t.Fatalf("session ID = %d, want 99", sessionID)
	}
}

func TestPerformHandshakeFinalTimeout(t *testing.T) {
	serverConn, clientConn := newHandshakeTestConnections(t)
	serverErrCh := make(chan error, 1)

	go func() {
		if err := serverConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			serverErrCh <- err
			return
		}
		for range requestAttempts {
			if _, _, err := udp.ReadVoicePacket(serverConn); err != nil {
				serverErrCh <- err
				return
			}
		}
		serverErrCh <- nil
	}()

	_, err := performHandshakeWithRequestID(
		context.Background(),
		clientConn,
		"alice",
		17,
		10*time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "timed out after 3 attempts") {
		t.Fatalf("performHandshakeWithRequestID() error = %v, want final timeout", err)
	}
	if serverErr := <-serverErrCh; serverErr != nil {
		t.Fatal(serverErr)
	}
}

func newHandshakeTestConnections(t *testing.T) (*net.UDPConn, *net.UDPConn) {
	t.Helper()

	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.ParseIP("127.0.0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}

	clientConn, err := net.DialUDP(
		"udp4",
		nil,
		serverConn.LocalAddr().(*net.UDPAddr),
	)
	if err != nil {
		_ = serverConn.Close()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return serverConn, clientConn
}

func TestJoinChannelSuccess(t *testing.T) {
	peer := newJoinTestPeer(t)
	serverErrCh := make(chan error, 1)

	go func() {
		request, addr, err := peer.receiveRequest()
		if err != nil {
			serverErrCh <- err
			return
		}
		serverErrCh <- peer.sendResponse(addr, protocol.VoicePacket{
			Type:      protocol.PacketJoinChannelAck,
			SessionID: request.SessionID,
			RequestID: request.RequestID,
			Payload:   request.Payload,
		})
	}()

	if err := JoinChannel(peer.ctx, peer.clientConn, peer.state, "music"); err != nil {
		t.Fatal(err)
	}
	if err := <-serverErrCh; err != nil {
		t.Fatal(err)
	}
	if got := peer.state.Channel(); got != "music" {
		t.Fatalf("State.Channel() = %q, want %q", got, "music")
	}
}

func TestJoinChannelServerError(t *testing.T) {
	peer := newJoinTestPeer(t)
	serverErrCh := make(chan error, 1)

	go func() {
		request, addr, err := peer.receiveRequest()
		if err != nil {
			serverErrCh <- err
			return
		}
		serverErrCh <- peer.sendResponse(
			addr,
			protocol.NewErrorPacket(
				request.SessionID,
				request.RequestID,
				"channel is unavailable",
			),
		)
	}()

	err := JoinChannel(peer.ctx, peer.clientConn, peer.state, "music")
	if err == nil || !strings.Contains(err.Error(), "channel is unavailable") {
		t.Fatalf("JoinChannel() error = %v, want server error", err)
	}
	if serverErr := <-serverErrCh; serverErr != nil {
		t.Fatal(serverErr)
	}
	if got := peer.state.Channel(); got != "" {
		t.Fatalf("State.Channel() = %q after server error, want empty", got)
	}
}

func TestJoinChannelRetriesLostAcknowledgement(t *testing.T) {
	peer := newJoinTestPeer(t)
	serverErrCh := make(chan error, 1)

	go func() {
		first, _, err := peer.receiveRequest()
		if err != nil {
			serverErrCh <- err
			return
		}

		second, addr, err := peer.receiveRequest()
		if err != nil {
			serverErrCh <- err
			return
		}
		if second.RequestID != first.RequestID {
			serverErrCh <- fmt.Errorf(
				"retry RequestID = %d, want %d",
				second.RequestID,
				first.RequestID,
			)
			return
		}

		serverErrCh <- peer.sendResponse(addr, protocol.VoicePacket{
			Type:      protocol.PacketJoinChannelAck,
			SessionID: second.SessionID,
			RequestID: second.RequestID,
			Payload:   second.Payload,
		})
	}()

	err := joinChannelWithTimeout(
		peer.ctx,
		peer.clientConn,
		peer.state,
		"music",
		20*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if serverErr := <-serverErrCh; serverErr != nil {
		t.Fatal(serverErr)
	}
}

func TestJoinChannelFinalTimeout(t *testing.T) {
	peer := newJoinTestPeer(t)
	serverErrCh := make(chan error, 1)

	go func() {
		for range 3 {
			if _, _, err := peer.receiveRequest(); err != nil {
				serverErrCh <- err
				return
			}
		}
		serverErrCh <- nil
	}()

	err := joinChannelWithTimeout(
		peer.ctx,
		peer.clientConn,
		peer.state,
		"music",
		10*time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "timed out after 3 attempts") {
		t.Fatalf("JoinChannel() error = %v, want final timeout", err)
	}
	if serverErr := <-serverErrCh; serverErr != nil {
		t.Fatal(serverErr)
	}
	if completed := peer.state.CompleteRequest(ControlResponse{
		Type:      protocol.PacketJoinChannelAck,
		RequestID: 1,
		Payload:   []byte("music"),
	}); completed {
		t.Fatal("late acknowledgement completed an already timed-out request")
	}
}

func TestValidateJoinResponse(t *testing.T) {
	tests := []struct {
		name      string
		response  ControlResponse
		requested string
		wantError string
	}{
		{
			name: "matching acknowledgement",
			response: ControlResponse{
				Type:    protocol.PacketJoinChannelAck,
				Payload: []byte("music"),
			},
			requested: "music",
		},
		{
			name: "unexpected response type",
			response: ControlResponse{
				Type: protocol.PacketHeartbeat,
			},
			requested: "music",
			wantError: "unexpected join response",
		},
		{
			name: "different channel",
			response: ControlResponse{
				Type:    protocol.PacketJoinChannelAck,
				Payload: []byte("gaming"),
			},
			requested: "music",
			wantError: "channel mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJoinResponse(tt.response, tt.requested)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("validateJoinResponse() error = %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("validateJoinResponse() error = nil, want %q", tt.wantError)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("validateJoinResponse() error = %q, want substring %q", err, tt.wantError)
			}
		})
	}
}
