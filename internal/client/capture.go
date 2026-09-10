package client

import (
	"context"
	"errors"
	"io"
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
			frame := audio.Frame{Data: buffer, Duration: pcmFrame.Duration}
			select {
			case audioCh <- frame:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func SendLoop(
	ctx context.Context,
	conn *udp.ClientPacketConn,
	sessionID uint64,
	audioCh <-chan audio.Frame,
) error {
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
			if err := conn.SendPacket(sent); err != nil {
				return err
			}
			sequence++
		}
	}
}

func RecordLoop(
	ctx context.Context,
	recorder audio.Recorder,
	pcmCh chan<- audio.PCMFrame,
	samplesPerFrame int,
) error {
	defer close(pcmCh)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		samples := make([]int16, samplesPerFrame)
		n, err := recorder.Read(samples)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		frame := audio.PCMFrame{
			Samples:  samples[:n],
			Duration: frameDuration,
		}
		select {
		case pcmCh <- frame:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
