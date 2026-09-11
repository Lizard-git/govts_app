package client

import (
	"context"
	"fmt"
	"log"
	"time"

	"example.com/go-voice-mvp/internal/domain"
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
			case protocol.PacketJoinChannelAck, protocol.PacketStateSnapshotAck:
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
	conn *udp.ClientPacketConn,
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
			if err := conn.SendPacket(packet); err != nil {
				return err
			}
		}
	}
}

func Disconnect(conn *udp.ClientPacketConn, sessionID uint64) error {
	packet := protocol.VoicePacket{
		Type:      protocol.PacketDisconnect,
		SessionID: sessionID,
	}
	return conn.SendPacket(packet)
}

func JoinChannel(
	ctx context.Context,
	conn *udp.ClientPacketConn,
	state *State,
	channelID domain.ChannelID,
) error {
	return joinChannelWithTimeout(ctx, conn, state, channelID, joinRequestTimeout)
}

func joinChannelWithTimeout(
	ctx context.Context,
	conn *udp.ClientPacketConn,
	state *State,
	channelID domain.ChannelID,
	timeout time.Duration,
) error {
	payload, err := protocol.EncodeJoinChannelRequest(channelID)
	if err != nil {
		return err
	}

	packet := protocol.VoicePacket{
		Type:    protocol.PacketJoinChannel,
		Payload: payload,
	}
	response, err := DoRequest(ctx, conn, state, packet, timeout)
	if err != nil {
		return fmt.Errorf("join channel %d: %w", channelID, err)
	}
	if err := validateJoinResponse(response, channelID); err != nil {
		return err
	}

	state.SetChannelID(channelID)
	return nil
}

func validateJoinResponse(response ControlResponse, requestedChannel domain.ChannelID) error {
	if response.Type != protocol.PacketJoinChannelAck {
		return fmt.Errorf("unexpected join response: type=%d", response.Type)
	}
	confirmedChannel, _, err := protocol.DecodeJoinChannelAck(response.Payload)
	if err != nil {
		return fmt.Errorf("invalid join response: %w", err)
	}
	if confirmedChannel != requestedChannel {
		return fmt.Errorf(
			"join response channel mismatch: got %d, want %d",
			confirmedChannel,
			requestedChannel,
		)
	}
	return nil
}
