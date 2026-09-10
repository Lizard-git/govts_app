package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"example.com/go-voice-mvp/internal/audio"
	voiceclient "example.com/go-voice-mvp/internal/client"
	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

const (
	sampleRate      = 48000
	channels        = 1
	samplesPerFrame = 960
)

func runSession(
	parent context.Context,
	conn *udp.ClientPacketConn,
	sessionID uint64,
	name string,
	channel string,
) (runErr error) {
	state := voiceclient.NewState(sessionID, name, "")

	audioCh := make(chan audio.Frame)
	pcmCh := make(chan audio.PCMFrame)
	encodedInCh := make(chan audio.MediaFrame)
	orderedInCh := make(chan audio.MediaFrame)
	controlCh := make(chan protocol.VoicePacket, 16)
	decodedCh := make(chan audio.MediaPCMFrame)
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

	supervisor := newLoopSupervisor(parent)
	ctx := supervisor.Context()
	defer supervisor.Cancel()

	// Receive and control loops must be running before JoinChannel: DoRequest
	// receives its acknowledgement through this part of the pipeline.
	supervisor.Go(func(ctx context.Context) error {
		// UDP → receiveLoop → encodedInCh → jitterLoop → orderedInCh
		return voiceclient.DecodeLoop(ctx, func() (audio.Decoder, error) {
			return audio.NewOpusDecoder(codecConfig)
		}, orderedInCh, decodedCh)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.JitterLoop(
			ctx,
			encodedInCh,
			orderedInCh,
			voiceclient.DefaultJitterDepth,
		)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.HeartbeatLoop(ctx, conn, sessionID)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.ReceiveLoop(ctx, conn, encodedInCh, controlCh)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.MixLoop(ctx, decodedCh, pcmOutCh, 20*time.Millisecond)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.PlaybackLoop(ctx, player, pcmOutCh)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.ControlLoop(ctx, state, controlCh)
	})

	log.Printf("client connected: id=%d name=%s", sessionID, name)
	if err := voiceclient.JoinChannel(ctx, conn, state, channel); err != nil {
		shutdownErr := supervisor.Shutdown(func() error {
			closeErr := player.Close()
			playerClosed = true
			return wrapError("close audio player", closeErr)
		})
		disconnectErr := voiceclient.Disconnect(conn, sessionID)
		return errors.Join(
			fmt.Errorf("join channel: %w", err),
			shutdownErr,
			wrapError("send disconnect", disconnectErr),
		)
	}
	log.Printf("join confirmed: %s", channel)

	// Capture starts only after the server has confirmed the channel. Therefore
	// no microphone frames can enter SendLoop before a successful join.
	recorder, err := audio.NewMalgoRecorder(codecConfig)
	if err != nil {
		shutdownErr := supervisor.Shutdown(func() error {
			closeErr := player.Close()
			playerClosed = true
			return wrapError("close audio player", closeErr)
		})
		disconnectErr := voiceclient.Disconnect(conn, sessionID)
		return errors.Join(
			fmt.Errorf("create audio recorder: %w", err),
			shutdownErr,
			wrapError("send disconnect", disconnectErr),
		)
	}

	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.EncodeLoop(ctx, encoder, pcmCh, audioCh)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.RecordLoop(ctx, recorder, pcmCh, samplesPerFrame)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.SendLoop(ctx, conn, sessionID, audioCh)
	})
	go voiceclient.CommandLoop(ctx, conn, state, supervisor.Cancel)

	runtimeErr := supervisor.Wait(func() error {
		recorderErr := recorder.Close()
		playerErr := player.Close()
		playerClosed = true

		return errors.Join(
			wrapError("close audio recorder", recorderErr),
			wrapError("close audio player", playerErr),
		)
	})

	disconnectErr := voiceclient.Disconnect(conn, sessionID)
	return errors.Join(runtimeErr, wrapError("send disconnect", disconnectErr))
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
