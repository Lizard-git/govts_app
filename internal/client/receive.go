package client

import (
	"context"
	"errors"
	"net"
	"time"

	"example.com/go-voice-mvp/internal/audio"
	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

const receivePollInterval = 500 * time.Millisecond

func ReceiveLoop(
	ctx context.Context,
	conn *udp.ClientPacketConn,
	encodedCh chan<- audio.MediaFrame,
	controlCh chan<- protocol.VoicePacket,
) error {
	defer close(encodedCh)
	defer close(controlCh)

	for {
		if err := conn.SetReadDeadline(time.Now().Add(receivePollInterval)); err != nil {
			return err
		}

		packet, err := conn.ReceivePacket()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					continue
				}
			}
			if errors.Is(err, protocol.ErrRejectedDatagram) {
				continue
			}
			return err
		}

		switch packet.Type {
		case protocol.PacketVoice:
			frame := audio.MediaFrame{
				SenderID: packet.SessionID,
				Sequence: packet.Sequence,
				Data:     packet.Payload,
				Duration: frameDuration,
			}
			select {
			case encodedCh <- frame:
			case <-ctx.Done():
				return ctx.Err()
			}
		default:
			select {
			case controlCh <- packet:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
