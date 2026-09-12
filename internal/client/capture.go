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
	states ...*State,
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
			if len(states) > 0 {
				muted, _, epoch := states[0].Audio.Snapshot()
				if muted || epoch != pcmFrame.ControlEpoch {
					continue
				}
				states[0].ObserveSpeaking(states[0].SessionID(), pcmFrame.Samples, time.Now())
			}
			buffer, err := encoder.Encode(pcmFrame.Samples)
			if err != nil {
				return err
			}
			frame := audio.Frame{Data: buffer, Duration: pcmFrame.Duration, ControlEpoch: pcmFrame.ControlEpoch}
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
	controls ...*AudioControlState,
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
			send := func() error { return conn.SendPacket(sent) }
			var err error
			if len(controls) > 0 {
				err = controls[0].Send(frame.ControlEpoch, send)
			} else {
				err = send()
			}
			if err != nil {
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
	controls ...*AudioControlState,
) error {
	defer close(pcmCh)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var epoch uint64
		if len(controls) > 0 {
			_, _, epoch = controls[0].Snapshot()
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
			ControlEpoch: epoch,
			Samples:      samples[:n],
			Duration:     frameDuration,
		}
		if len(controls) > 0 {
			muted, _, current := controls[0].Snapshot()
			if muted || current != epoch {
				continue
			}
		}
		select {
		case pcmCh <- frame:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
