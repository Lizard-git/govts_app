package client

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

const (
	handshakeRequestTimeout = 3 * time.Second
	joinRequestTimeout      = 3 * time.Second
	requestAttempts         = 3
	maxClientNameBytes      = 64
)

func PerformHandshake(
	ctx context.Context,
	conn *net.UDPConn,
	name string,
) (uint64, error) {
	if len(name) == 0 {
		return 0, errors.New("client name is required")
	}
	if len([]byte(name)) > maxClientNameBytes {
		return 0, fmt.Errorf("client name too long: %d bytes", len([]byte(name)))
	}

	requestID, err := newHandshakeRequestID()
	if err != nil {
		return 0, fmt.Errorf("create handshake request ID: %w", err)
	}

	return performHandshakeWithRequestID(
		ctx,
		conn,
		name,
		requestID,
		handshakeRequestTimeout,
	)
}

func newHandshakeRequestID() (uint32, error) {
	for {
		var data [4]byte
		if _, err := rand.Read(data[:]); err != nil {
			return 0, err
		}

		requestID := binary.BigEndian.Uint32(data[:])
		if requestID != 0 {
			return requestID, nil
		}
	}
}

func performHandshakeWithRequestID(
	ctx context.Context,
	conn *net.UDPConn,
	name string,
	requestID uint32,
	timeout time.Duration,
) (uint64, error) {
	if requestID == 0 {
		return 0, errors.New("handshake request ID must not be zero")
	}
	if timeout <= 0 {
		return 0, errors.New("handshake timeout must be positive")
	}

	hello := protocol.VoicePacket{
		Type:      protocol.PacketHello,
		RequestID: requestID,
		Payload:   []byte(name),
	}

	defer conn.SetReadDeadline(time.Time{})

	for attempt := 1; attempt <= requestAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		deadline := time.Now().Add(timeout)
		if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
			deadline = contextDeadline
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return 0, err
		}

		if err := udp.SendPacket(conn, hello); err != nil {
			return 0, fmt.Errorf("send handshake request %d: %w", requestID, err)
		}

		ack, err := udp.ReceivePacket(conn)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return 0, ctxErr
				}
				if attempt < requestAttempts {
					continue
				}
				return 0, fmt.Errorf(
					"handshake request %d timed out after %d attempts",
					requestID,
					requestAttempts,
				)
			}
			return 0, err
		}

		if ack.RequestID != requestID {
			return 0, fmt.Errorf(
				"unexpected handshake request ID: got %d, want %d",
				ack.RequestID,
				requestID,
			)
		}
		if ack.Type == protocol.PacketError {
			return 0, fmt.Errorf("server error: %s", string(ack.Payload))
		}
		if ack.Type != protocol.PacketHelloAck {
			return 0, fmt.Errorf("expected hello ack, got packet type %d", ack.Type)
		}
		if ack.SessionID == 0 {
			return 0, errors.New("server returned invalid session ID")
		}

		return ack.SessionID, nil
	}

	return 0, errors.New("handshake failed")
}

func DoRequest(
	ctx context.Context,
	conn *net.UDPConn,
	state *State,
	packet protocol.VoicePacket,
	timeout time.Duration,
) (ControlResponse, error) {
	requestID := state.NextRequestID()
	packet.SessionID = state.SessionID()
	packet.RequestID = requestID

	responseCh := state.RegisterRequest(requestID)
	defer state.CancelRequest(requestID)

	for attempt := 1; attempt <= requestAttempts; attempt++ {
		if err := udp.SendPacket(conn, packet); err != nil {
			return ControlResponse{}, fmt.Errorf("send request %d: %w", requestID, err)
		}

		timer := time.NewTimer(timeout)
		select {
		case response := <-responseCh:
			timer.Stop()
			if response.Type == protocol.PacketError {
				return ControlResponse{}, fmt.Errorf("server error: %s", string(response.Payload))
			}
			return response, nil
		case <-timer.C:
			if attempt == requestAttempts {
				return ControlResponse{}, fmt.Errorf(
					"request %d timed out after %d attempts",
					requestID,
					requestAttempts,
				)
			}
		case <-ctx.Done():
			timer.Stop()
			return ControlResponse{}, ctx.Err()
		}
	}
	return ControlResponse{}, fmt.Errorf("request %d failed", requestID)
}
