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

const frameDuration = 20 * time.Millisecond

func EncodeLoop(
	ctx context.Context,
	encoder audio.Encoder,
	pcmCh <-chan audio.PCMFrame,
	audioCh chan<- audio.Frame,
) error {
	defer close(audioCh)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case pcmFrame, ok := <-pcmCh:
			if !ok {
				return nil
			}
			buffer, err := encoder.Encode(pcmFrame.Samples)
			if err != nil {
				return err
			}
			frame := audio.Frame{
				Data:     buffer,
				Duration: pcmFrame.Duration,
			}
			select {
			case audioCh <- frame:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func SendLoop(ctx context.Context, conn *net.UDPConn, sessionID uint64, audioCh <-chan audio.Frame) error {
	sequence := uint32(1)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case frame, ok := <-audioCh:
			if !ok {
				return nil
			}
			sent := protocol.NewVoicePacket(sessionID, sequence, frame.Data)
			if err := udp.SendPacket(conn, sent); err != nil {
				return err
			}
			sequence++
		}
	}
}

func ReceiveLoop(
	ctx context.Context,
	conn *net.UDPConn,
	encodedCh chan<- audio.Frame,
) error {
	defer close(encodedCh)

	for {
		if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			return err
		}

		packet, err := udp.ReceivePacket(conn)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					continue
				}
			} else {
				return err
			}
		}

		frame := audio.Frame{
			Data:     packet.Payload,
			Duration: frameDuration,
		}

		select {
		case encodedCh <- frame:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func DecodeLoop(
	ctx context.Context,
	decoder audio.Decoder,
	encodedCh <-chan audio.Frame,
	pcmOutCh chan<- audio.PCMFrame,
) error {
	defer close(pcmOutCh)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case frame, ok := <-encodedCh:
			if !ok {
				return nil
			}
			samples, err := decoder.Decode(frame.Data)
			if err != nil {
				return err
			}
			pcmFrame := audio.PCMFrame{
				Samples:  samples,
				Duration: frame.Duration,
			}
			select {
			case pcmOutCh <- pcmFrame:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func PlaybackLoop(ctx context.Context, player audio.Player, pcmOutCh <-chan audio.PCMFrame) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case frame, ok := <-pcmOutCh:
			if !ok {
				return nil
			}

			if err := player.Write(frame.Samples); err != nil {
				return err
			}
			// fmt.Printf("decoded PCM: samples=%d duration=%s", len(frame.Samples), frame.Duration)
		}
	}
}
