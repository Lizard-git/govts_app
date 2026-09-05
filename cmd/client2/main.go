package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/go-voice-mvp/internal/audio"
	voiceclient "example.com/go-voice-mvp/internal/client"
	"example.com/go-voice-mvp/internal/transport/udp"
)

const (
	frameDuration   = 20 * time.Millisecond
	sampleRate      = 48000
	channels        = 1
	samplesPerFrame = 960
)

func main() {
	name := flag.String("name", "", "client name")
	flag.Parse()
	if *name == "" {
		log.Fatal("client name required")
	}

	conn, err := udp.ConnectUDP("127.0.0.1", 9000)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	sessionID, err := voiceclient.PerformHandshake(conn, *name)
	if err != nil {
		log.Fatalf("handshake failed: %v", err)
	}
	log.Printf(
		"client connected: id=%d name=%s",
		sessionID,
		*name,
	)

	errCh := make(chan error, 7)
	signalCtx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	ctx, cancel := context.WithCancel(signalCtx)
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
		errCh <- voiceclient.RecordLoop(ctx, recorder, pcmCh, samplesPerFrame)
	}()
	go func() {
		errCh <- voiceclient.SendLoop(ctx, conn, sessionID, audioCh)
	}()
	go func() {
		errCh <- voiceclient.HeartbeatLoop(ctx, conn, sessionID)
	}()
	go func() {
		errCh <- voiceclient.ReceiveLoop(ctx, conn, encodedInCh)
	}()
	go func() {
		errCh <- voiceclient.PlaybackLoop(ctx, player, pcmOutCh)
	}()

	firstErr := <-errCh
	cancel()
	_ = recorder.Close()
	if err := voiceclient.Disconnect(conn, sessionID); err != nil {
		log.Printf("send disconnect: %v", err)
	}

	for range 6 {
		err := <-errCh
		logLoopError("client stopped", err)
	}

	logLoopError("client stopped", firstErr)
}

func logLoopError(prefix string, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("%s: %v", prefix, err)
	}
}
