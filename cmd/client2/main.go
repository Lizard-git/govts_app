package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"time"

	"example.com/go-voice-mvp/internal/audio"
	"example.com/go-voice-mvp/internal/protocol"
)

const (
	frameDuration   = 20 * time.Millisecond
	sampleRate      = 48000
	channels        = 1
	samplesPerFrame = 960
)

func main() {
	conn, err := protocol.ConnectUDP("127.0.0.1", 9000)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	hello := protocol.VoicePacket{
		Type:      protocol.PacketHello,
		SessionID: 42,
	}
	if err := protocol.SendPacket(conn, hello); err != nil {
		log.Fatal(err)
	}

	errCh := make(chan error, 5)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	audioCh := make(chan audio.Frame)
	pcmCh := make(chan audio.PCMFrame)
	encodedInCh := make(chan audio.Frame)
	pcmOutCh := make(chan audio.PCMFrame)

	codecConfig := audio.CodecConfig{
		SampleRate:      sampleRate,
		Channels:        channels,
		SamplesPerFrame: samplesPerFrame,
	}

	encoder, err := audio.NewOpusEncoder(codecConfig)
	if err != nil {
		log.Fatalf("create Opus encoder: %v", err)
	}

	decoder, err := audio.NewOpusDecoder(codecConfig)
	if err != nil {
		log.Fatalf("create Opus decoder: %v", err)
	}

	player, err := audio.NewOtoPlayer(codecConfig)
	if err != nil {
		log.Fatalf("create audio player: %v", err)
	}
	defer player.Close()

	go func() {
		errCh <- encodeLoop(ctx, encoder, pcmCh, audioCh)
	}()
	go func() {
		// UDP → receiveLoop → encodedInCh → decodeLoop → pcmOutCh
		errCh <- decodeLoop(ctx, decoder, encodedInCh, pcmOutCh)
	}()
	go produceTestAudio(ctx, pcmCh)
	go func() {
		errCh <- sendLoop(ctx, conn, 42, audioCh)
	}()
	go func() {
		errCh <- receiveLoop(ctx, conn, encodedInCh)
	}()
	go func() {
		errCh <- playbackLoop(ctx, player, pcmOutCh)
	}()

	err1 := <-errCh
	cancel()
	err2 := <-errCh
	err3 := <-errCh
	err4 := <-errCh
	err5 := <-errCh

	logLoopError("client stopped", err1)
	logLoopError("client stopped", err2)
	logLoopError("client stopped", err3)
	logLoopError("client stopped", err4)
	logLoopError("client stopped", err5)
}

func logLoopError(prefix string, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("%s: %v", prefix, err)
	}
}

func produceTestAudio(
	ctx context.Context,
	pcmCh chan<- audio.PCMFrame,
) {
	defer close(pcmCh)

	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	const frequency = 440.0
	const amplitude = 10000.0
	phase := 0.0
	phaseStep := 2 * math.Pi * frequency / float64(sampleRate)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			samples := make([]int16, samplesPerFrame)

			for i := range samples {
				samples[i] = int16(amplitude * math.Sin(phase))

				phase += phaseStep

				if phase >= 2*math.Pi {
					phase -= 2 * math.Pi
				}
			}

			pcmFrame := audio.PCMFrame{
				Samples:  samples,
				Duration: frameDuration,
			}
			select {
			case pcmCh <- pcmFrame:
			case <-ctx.Done():
				return
			}
		}
	}
}

func encodeLoop(
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

func sendLoop(ctx context.Context, conn *net.UDPConn, sessionID uint64, audioCh <-chan audio.Frame) error {
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
			if err := protocol.SendPacket(conn, sent); err != nil {
				return err
			}
			sequence++
		}
	}
}

func receiveLoop(
	ctx context.Context,
	conn *net.UDPConn,
	encodedCh chan<- audio.Frame,
) error {
	defer close(encodedCh)

	for {
		if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			return err
		}

		packet, err := protocol.ReceivePacket(conn)
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

func decodeLoop(
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

func playbackLoop(ctx context.Context, player audio.Player, pcmOutCh <-chan audio.PCMFrame) error {
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
			fmt.Printf("decoded PCM: samples=%d duration=%s", len(frame.Samples), frame.Duration)
		}
	}
}
