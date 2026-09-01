package main

import (
	"context"
	"errors"
	"log"
	"math"
	"time"

	"example.com/go-voice-mvp/internal/audio"
	voiceclient "example.com/go-voice-mvp/internal/client"
	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

const (
	frameDuration   = 20 * time.Millisecond
	sampleRate      = 48000
	channels        = 1
	samplesPerFrame = 960
)

func main() {
	conn, err := udp.ConnectUDP("127.0.0.1", 9000)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	hello := protocol.VoicePacket{
		Type:      protocol.PacketHello,
		SessionID: 42,
	}
	if err := udp.SendPacket(conn, hello); err != nil {
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
		errCh <- voiceclient.EncodeLoop(ctx, encoder, pcmCh, audioCh)
	}()
	go func() {
		// UDP → receiveLoop → encodedInCh → decodeLoop → pcmOutCh
		errCh <- voiceclient.DecodeLoop(ctx, decoder, encodedInCh, pcmOutCh)
	}()
	go produceTestAudio(ctx, pcmCh)
	go func() {
		errCh <- voiceclient.SendLoop(ctx, conn, 42, audioCh)
	}()
	go func() {
		errCh <- voiceclient.ReceiveLoop(ctx, conn, encodedInCh)
	}()
	go func() {
		errCh <- voiceclient.PlaybackLoop(ctx, player, pcmOutCh)
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
