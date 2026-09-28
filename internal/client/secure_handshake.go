package client

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

	"uniclog.io/govts/internal/appversion"
	"uniclog.io/govts/internal/auth"
	"uniclog.io/govts/internal/identity"
	"uniclog.io/govts/internal/protocol"
	"uniclog.io/govts/internal/transport/udp"
)

type SecureHandshakeResult struct {
	SessionID   uint64
	JoinLevel   uint16
	Permissions uint8
}

type AuthenticationRejectedError struct{ Reason string }

func (e *AuthenticationRejectedError) Error() string {
	return "server rejected authentication: " + e.Reason
}

func PerformSecureHandshakeAttempts(ctx context.Context, conn *udp.ClientPacketConn, codec *protocol.SecureDatagramCodec, name string, private ed25519.PrivateKey, pinPath, endpoint string, timeout time.Duration, attempts int, minimumVersion string) (SecureHandshakeResult, error) {
	if conn == nil || codec == nil || timeout <= 0 || attempts <= 0 {
		return SecureHandshakeResult{}, errors.New("invalid secure handshake arguments")
	}
	version, err := appversion.Parse(minimumVersion)
	if err != nil {
		return SecureHandshakeResult{}, err
	}
	requestID, err := newHandshakeRequestID()
	if err != nil {
		return SecureHandshakeResult{}, err
	}
	init, ephemeral, err := auth.NewInit(name, private)
	if err != nil {
		return SecureHandshakeResult{}, err
	}
	initPacket := protocol.VoicePacket{Type: protocol.PacketAuthInit, RequestID: requestID, Sequence: uint32(version), Payload: init.Encode()}
	challengePacket, err := exchangeAuth(ctx, conn, initPacket, protocol.PacketAuthChallenge, requestID, timeout, attempts, version)
	if err != nil {
		return SecureHandshakeResult{}, err
	}
	if challengePacket.Sequence != uint32(version) {
		return SecureHandshakeResult{}, errors.New("authentication challenge version mismatch")
	}
	challenge, err := auth.DecodeChallenge(challengePacket.Payload)
	if err != nil {
		return SecureHandshakeResult{}, err
	}
	if err := auth.VerifyChallenge(init, challenge, requestID, uint32(version)); err != nil {
		return SecureHandshakeResult{}, err
	}
	if err := identity.CheckOrTrust(pinPath, endpoint, ed25519.PublicKey(challenge.ServerKey[:])); err != nil {
		return SecureHandshakeResult{}, err
	}
	digest := auth.Transcript(init, challenge, requestID, uint32(version))
	c2s, s2c, ackKey, err := auth.DeriveKeys(ephemeral, challenge.Ephemeral, digest)
	if err != nil {
		return SecureHandshakeResult{}, err
	}
	finish := protocol.VoicePacket{Type: protocol.PacketAuthFinish, RequestID: requestID, Sequence: uint32(version), Payload: auth.SignFinish(private, digest)}
	ack, err := exchangeAuth(ctx, conn, finish, protocol.PacketAuthAck, requestID, timeout, attempts, version)
	if err != nil {
		return SecureHandshakeResult{}, err
	}
	if ack.SessionID == 0 || ack.Sequence != uint32(version) || len(ack.Payload) != 35 {
		return SecureHandshakeResult{}, errors.New("invalid authentication acknowledgement")
	}
	level := binary.BigEndian.Uint16(ack.Payload[32:34])
	permissions := ack.Payload[34]
	want := auth.AckMAC(ackKey, digest, ack.SessionID, level, permissions)
	if !hmac.Equal(ack.Payload[:32], want[:]) {
		return SecureHandshakeResult{}, errors.New("authentication acknowledgement failed verification")
	}
	if err := codec.Install(ack.SessionID, c2s, s2c); err != nil {
		return SecureHandshakeResult{}, err
	}
	return SecureHandshakeResult{SessionID: ack.SessionID, JoinLevel: level, Permissions: permissions}, nil
}

func exchangeAuth(ctx context.Context, conn *udp.ClientPacketConn, request protocol.VoicePacket, wantType uint8, requestID uint32, timeout time.Duration, attempts int, minimum appversion.Number) (protocol.VoicePacket, error) {
	defer conn.SetReadDeadline(time.Time{})
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return protocol.VoicePacket{}, err
		}
		deadline := time.Now().Add(timeout)
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return protocol.VoicePacket{}, err
		}
		if err := conn.SendPacket(request); err != nil {
			return protocol.VoicePacket{}, err
		}
		for {
			response, err := conn.ReceivePacket()
			if err != nil {
				if errors.Is(err, protocol.ErrRejectedDatagram) {
					continue
				}
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					break
				}
				return protocol.VoicePacket{}, err
			}
			if response.RequestID != requestID {
				continue
			}
			if response.Type == protocol.PacketServerVersionTooOld {
				return protocol.VoicePacket{}, &ServerVersionTooOldError{Server: appversion.Number(response.Sequence), Required: minimum}
			}
			if response.Type == protocol.PacketError {
				return protocol.VoicePacket{}, &AuthenticationRejectedError{Reason: string(response.Payload)}
			}
			if response.Type == wantType {
				return response, nil
			}
		}
	}
	return protocol.VoicePacket{}, fmt.Errorf("secure handshake phase %d timed out", request.Type)
}
