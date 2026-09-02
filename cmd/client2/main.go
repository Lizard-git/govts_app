package main

import (
	"context"
	"errors"
	"io"
	"log"
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

	errCh := make(chan error, 6)
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

	recorder, err := audio.NewMalgoRecorder(codecConfig)
	if err != nil {
		log.Fatalf("create audio recorder: %v", err)
	}

	go func() {
		errCh <- voiceclient.EncodeLoop(ctx, encoder, pcmCh, audioCh)
	}()
	go func() {
		// UDP → receiveLoop → encodedInCh → decodeLoop → pcmOutCh
		errCh <- voiceclient.DecodeLoop(ctx, decoder, encodedInCh, pcmOutCh)
	}()
	go func() {
		errCh <- recordLoop(ctx, recorder, pcmCh)
	}()
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
	_ = recorder.Close()

	err2 := <-errCh
	err3 := <-errCh
	err4 := <-errCh
	err5 := <-errCh
	err6 := <-errCh

	logLoopError("client stopped", err1)
	logLoopError("client stopped", err2)
	logLoopError("client stopped", err3)
	logLoopError("client stopped", err4)
	logLoopError("client stopped", err5)
	logLoopError("client stopped", err6)
}

func logLoopError(prefix string, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("%s: %v", prefix, err)
	}
}

func recordLoop(
	ctx context.Context,
	recorder audio.Recorder,
	pcmCh chan<- audio.PCMFrame,
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
