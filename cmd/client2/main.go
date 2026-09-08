package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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

	if err := run(*name, *channel); err != nil {
		log.Fatal(err)
	}
}

func run(name string, channel string) (runErr error) {
	if name == "" {
		return errors.New("client name required")
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := udp.ConnectUDP("127.0.0.1", 9000)
	if err != nil {
		return fmt.Errorf("connect UDP: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close UDP connection: %w", err))
		}
	}()

	sessionID, err := voiceclient.PerformHandshake(signalCtx, conn, name)
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	state := voiceclient.NewState(
		sessionID,
		name,
		"",
	)

	const loopCount = 8
	errCh := make(chan error, loopCount)

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
		return fmt.Errorf("create Opus encoder: %w", err)
	}

	decoder, err := audio.NewOpusDecoder(codecConfig)
	if err != nil {
		return fmt.Errorf("create Opus decoder: %w", err)
	}

	player, err := audio.NewOtoPlayer(codecConfig)
	if err != nil {
		return fmt.Errorf("create audio player: %w", err)
	}
	playerClosed := false
	defer func() {
		if !playerClosed {
			if err := player.Close(); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("close audio player: %w", err))
			}
		}
	}()

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

	log.Printf("client connected: id=%d name=%s", sessionID, name)
	if err := voiceclient.JoinChannel(ctx, conn, state, channel); err != nil {
		cancel()
		disconnectErr := voiceclient.Disconnect(conn, sessionID)
		return errors.Join(
			fmt.Errorf("join channel: %w", err),
			wrapError("send disconnect", disconnectErr),
		)
	}
	log.Printf("join confirmed: %s", channel)

	// Capture starts only after the server has confirmed the channel. Therefore
	// no microphone frames can enter SendLoop before a successful join.
	recorder, err := audio.NewMalgoRecorder(codecConfig)
	if err != nil {
		cancel()
		disconnectErr := voiceclient.Disconnect(conn, sessionID)
		return errors.Join(
			fmt.Errorf("create audio recorder: %w", err),
			wrapError("send disconnect", disconnectErr),
		)
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
	recorderErr := recorder.Close()
	playerErr := player.Close()
	playerClosed = true

	loopErrors := make([]error, 0, loopCount)
	loopErrors = append(loopErrors, unexpectedLoopError(firstErr))
	for i := 1; i < loopCount; i++ {
		loopErrors = append(loopErrors, unexpectedLoopError(<-errCh))
	}

	disconnectErr := voiceclient.Disconnect(conn, sessionID)
	return errors.Join(
		errors.Join(loopErrors...),
		wrapError("close audio recorder", recorderErr),
		wrapError("close audio player", playerErr),
		wrapError("send disconnect", disconnectErr),
	)
}

func unexpectedLoopError(err error) error {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
