package client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

func ControlLoop(
	ctx context.Context,
	state *State,
	controlCh <-chan protocol.VoicePacket,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case packet, ok := <-controlCh:
			if !ok {
				return nil
			}
			if packet.SessionID != state.SessionID() {
				log.Printf("ignoring control packet for session %d", packet.SessionID)
				continue
			}

			switch packet.Type {
			case protocol.PacketJoinChannelAck:
				response := ControlResponse{
					Type:      packet.Type,
					RequestID: packet.RequestID,
					Payload:   packet.Payload,
				}
				if !state.CompleteRequest(response) {
					log.Printf("response for unknown request: request_id=%d", packet.RequestID)
				}
			case protocol.PacketError:
				response := ControlResponse{
					Type:      packet.Type,
					RequestID: packet.RequestID,
					Payload:   packet.Payload,
				}
				if !state.CompleteRequest(response) {
					log.Printf("error response for unknown request: request_id=%d", packet.RequestID)
				}
			default:
				log.Printf("unhandled control packet: type=%d", packet.Type)
			}
		}
	}
}

func HeartbeatLoop(
	ctx context.Context,
	conn *net.UDPConn,
	sessionID uint64,
) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			packet := protocol.VoicePacket{
				Type:      protocol.PacketHeartbeat,
				SessionID: sessionID,
			}
			if err := udp.SendPacket(conn, packet); err != nil {
				return err
			}
		}
	}
}

func Disconnect(conn *net.UDPConn, sessionID uint64) error {
	packet := protocol.VoicePacket{
		Type:      protocol.PacketDisconnect,
		SessionID: sessionID,
	}
	return udp.SendPacket(conn, packet)
}

func JoinChannel(
	ctx context.Context,
	conn *net.UDPConn,
	state *State,
	channel string,
) error {
	return joinChannelWithTimeout(ctx, conn, state, channel, joinRequestTimeout)
}

func joinChannelWithTimeout(
	ctx context.Context,
	conn *net.UDPConn,
	state *State,
	channel string,
	timeout time.Duration,
) error {
	if channel == "" {
		return errors.New("channel name is required")
	}

	packet := protocol.VoicePacket{
		Type:    protocol.PacketJoinChannel,
		Payload: []byte(channel),
	}
	response, err := DoRequest(ctx, conn, state, packet, timeout)
	if err != nil {
		return fmt.Errorf("join channel %q: %w", channel, err)
	}
	if err := validateJoinResponse(response, channel); err != nil {
		return err
	}

	state.SetChannel(channel)
	return nil
}

func validateJoinResponse(response ControlResponse, requestedChannel string) error {
	if response.Type != protocol.PacketJoinChannelAck {
		return fmt.Errorf("unexpected join response: type=%d", response.Type)
	}
	confirmedChannel := string(response.Payload)
	if confirmedChannel != requestedChannel {
		return fmt.Errorf(
			"join response channel mismatch: got %q, want %q",
			confirmedChannel,
			requestedChannel,
		)
	}
	return nil
}
