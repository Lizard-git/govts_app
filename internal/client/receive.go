package client

import (
	"context"
	"errors"
	"net"
	"time"

	"uniclog.io/govts/internal/audio"
	"uniclog.io/govts/internal/logging"
	"uniclog.io/govts/internal/protocol"
	"uniclog.io/govts/internal/transport/udp"
)

const receivePollInterval = 500 * time.Millisecond

func ReceiveLoop(
	ctx context.Context,
	conn *udp.ClientPacketConn,
	encodedCh chan<- audio.MediaFrame,
	controlCh chan<- protocol.VoicePacket,
	states ...*State,
) error {
	defer close(encodedCh)
	defer close(controlCh)
	rejected := logging.NewFailures("client_udp_decode")
	defer rejected.Close()

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
				rejected.RecordKind(protocol.DatagramFailureReason(err), err, conn.LocalAddr())
				continue
			}
			return err
		}

		switch packet.Type {
		case protocol.PacketVoice:
			if len(states) > 0 && states[0] != nil {
				states[0].RecordVoiceArrival(packet.SessionID, packet.Sequence)
			}
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
