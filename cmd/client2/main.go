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
	name := flag.String("name", "", "client name")
	channel := flag.String("channel", "default", "channel name")
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

	state := voiceclient.NewState(
		sessionID,
		*name,
		"",
	)

	const loopCount = 8
	errCh := make(chan error, loopCount)

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	audioCh := make(chan audio.Frame)
	pcmCh := make(chan audio.PCMFrame)
	encodedInCh := make(chan audio.Frame)
	controlCh := make(chan protocol.VoicePacket, 16)
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

	// Receive and control loops must be running before JoinChannel: DoRequest
	// receives its acknowledgement through this part of the pipeline.
	go func() {
		// UDP → receiveLoop → encodedInCh → decodeLoop → pcmOutCh
		errCh <- voiceclient.DecodeLoop(ctx, decoder, encodedInCh, pcmOutCh)
	}()
	go func() {
		errCh <- voiceclient.HeartbeatLoop(ctx, conn, sessionID)
	}()
	go func() {
		errCh <- voiceclient.ReceiveLoop(ctx, conn, encodedInCh, controlCh)
	}()
	go func() {
		errCh <- voiceclient.PlaybackLoop(ctx, player, pcmOutCh)
	}()
	go func() {
		errCh <- voiceclient.ControlLoop(ctx, state, controlCh)
	}()

	log.Printf("client connected: id=%d name=%s", sessionID, *name)
	if err := voiceclient.JoinChannel(ctx, conn, state, *channel); err != nil {
		cancel()
		_ = voiceclient.Disconnect(conn, sessionID)
		log.Printf("join channel failed: %v", err)
		return
	}
	log.Printf("join confirmed: %s", *channel)

	// Capture starts only after the server has confirmed the channel. Therefore
	// no microphone frames can enter SendLoop before a successful join.
	recorder, err := audio.NewMalgoRecorder(codecConfig)
	if err != nil {
		cancel()
		_ = voiceclient.Disconnect(conn, sessionID)
		log.Printf("create audio recorder: %v", err)
		return
	}

	go func() {
		errCh <- voiceclient.EncodeLoop(ctx, encoder, pcmCh, audioCh)
	}()
	go func() {
		errCh <- voiceclient.RecordLoop(ctx, recorder, pcmCh, samplesPerFrame)
	}()
	go func() {
		errCh <- voiceclient.SendLoop(ctx, conn, sessionID, audioCh)
	}()
	go voiceclient.CommandLoop(ctx, conn, state, cancel)

	firstErr := <-errCh

	cancel()
	_ = recorder.Close()

	if err := voiceclient.Disconnect(conn, sessionID); err != nil {
		log.Printf("send disconnect: %v", err)
	}

	for i := 1; i < loopCount; i++ {
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
