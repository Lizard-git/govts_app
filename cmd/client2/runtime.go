package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"example.com/go-voice-mvp/internal/audio"
	voiceclient "example.com/go-voice-mvp/internal/client"
	"example.com/go-voice-mvp/internal/domain"
	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

const (
	sampleRate      = 48000
	channels        = 1
	samplesPerFrame = 960
)

func runSession(parent context.Context, conn *udp.ClientPacketConn, state *voiceclient.State, playbackOutput *audio.OtoOutput, name string, preference *channelPreference, commands <-chan voiceclient.Command, output io.Writer, cancelApp context.CancelFunc, firstConnection bool, onReady func()) (runErr error) {
	sessionID := state.SessionID()
	oldSnapshot := state.Snapshot()
	audioCh := make(chan audio.Frame)
	pcmCh := make(chan audio.PCMFrame)
	encodedInCh := make(chan audio.MediaFrame)
	orderedInCh := make(chan audio.MediaFrame)
	controlCh := make(chan protocol.VoicePacket, 16)
	decodedCh := make(chan audio.MediaPCMFrame)
	pcmOutCh := make(chan audio.PCMFrame)
	codecConfig := clientAudioConfig()
	encoder, err := audio.NewOpusEncoder(codecConfig)
	if err != nil {
		return fmt.Errorf("create Opus encoder: %w", err)
	}
	player, err := playbackOutput.NewPlayer()
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
	networkLoop := func(operation string, loop func() error) error {
		return classifyNetworkLoopError(operation, loop())
	}
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.DecodeLoop(ctx, func() (audio.Decoder, error) { return audio.NewOpusDecoder(codecConfig) }, orderedInCh, decodedCh)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.JitterLoop(ctx, encodedInCh, orderedInCh, voiceclient.DefaultJitterDepth)
	})
	supervisor.Go(func(ctx context.Context) error {
		return networkLoop("heartbeat", func() error { return voiceclient.HeartbeatLoop(ctx, conn, state) })
	})
	supervisor.Go(func(ctx context.Context) error {
		return networkLoop("receive", func() error { return voiceclient.ReceiveLoop(ctx, conn, encodedInCh, controlCh) })
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.MixLoop(ctx, decodedCh, pcmOutCh, 20*time.Millisecond)
	})
	supervisor.Go(func(ctx context.Context) error { return voiceclient.PlaybackLoop(ctx, player, pcmOutCh) })
	supervisor.Go(func(ctx context.Context) error { return voiceclient.ControlLoop(ctx, state, controlCh) })
	joinedSignal := make(chan struct{}, 1)
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.SessionCommandLoop(ctx, conn, state, commands, output, cancelApp, func(locator voiceclient.ChannelLocator) {
			preference.Set(locator)
			select {
			case joinedSignal <- struct{}{}:
			default:
			}
		})
	})

	finish := func(cause error) error {
		shutdownErr := supervisor.Shutdown(func() error {
			closeErr := player.Close()
			playerClosed = true
			return wrapError("close audio player", closeErr)
		})
		return errors.Join(cause, shutdownErr)
	}
	log.Printf("client connected: id=%d name=%s", sessionID, name)
	snapshot, err := voiceclient.LoadServerSnapshot(ctx, conn, state)
	if err != nil {
		return finish(fmt.Errorf("%w: load server snapshot: %v", voiceclient.ErrConnectionLost, err))
	}
	selector, locator := preference.Get()
	var channelID domain.ChannelID
	treePrinted := false
	if len(locator) > 0 {
		id, resolveErr := voiceclient.ResolveChannelLocator(snapshot, locator)
		if resolveErr == nil {
			channelID = id
		} else {
			log.Printf("cannot restore channel: %v", resolveErr)
		}
	} else {
		id, resolveErr := voiceclient.ResolveChannel(snapshot, selector)
		if resolveErr == nil {
			channelID = id
		} else {
			log.Printf("cannot resolve initial channel: %v", resolveErr)
		}
	}
	if channelID != 0 {
		if err := voiceclient.JoinChannel(ctx, conn, state, channelID); err != nil {
			log.Printf("cannot join channel: %v", err)
			channelID = 0
		} else {
			snapshot, err = voiceclient.LoadServerSnapshot(ctx, conn, state)
			if err != nil {
				return finish(fmt.Errorf("%w: refresh server snapshot: %v", voiceclient.ErrConnectionLost, err))
			}
			saved, err := voiceclient.BuildChannelLocator(snapshot, channelID)
			if err != nil {
				return finish(err)
			}
			preference.Set(saved)
		}
	}
	if channelID == 0 {
		state.SetConnectionStatus(voiceclient.ConnectionConnected)
		_ = voiceclient.RenderServerTree(output, snapshot, sessionID, 0, false)
		treePrinted = true
		select {
		case <-ctx.Done():
			return finish(nil)
		case <-joinedSignal:
			channelID = state.ChannelID()
		}
	}
	currentSnapshot := state.Snapshot()
	topologyUnchanged := voiceclient.SameChannelTopology(oldSnapshot, currentSnapshot)
	if shouldRenderConnectionTree(firstConnection, treePrinted, topologyUnchanged) {
		if !firstConnection {
			log.Printf("reconnected; channel structure changed")
		}
		_ = voiceclient.RenderServerTree(output, currentSnapshot, sessionID, state.ChannelID(), false)
	} else if !firstConnection {
		log.Printf("reconnected; channel restored: id=%d", state.ChannelID())
	}
	state.SetConnectionStatus(voiceclient.ConnectionConnected)
	recorder, err := audio.NewMalgoRecorder(codecConfig)
	if err != nil {
		return finish(fmt.Errorf("create audio recorder: %w", err))
	}
	supervisor.Go(func(ctx context.Context) error { return voiceclient.EncodeLoop(ctx, encoder, pcmCh, audioCh) })
	supervisor.Go(func(ctx context.Context) error { return voiceclient.RecordLoop(ctx, recorder, pcmCh, samplesPerFrame) })
	supervisor.Go(func(ctx context.Context) error {
		return networkLoop("voice send", func() error { return voiceclient.SendLoop(ctx, conn, sessionID, audioCh) })
	})
	if onReady != nil {
		onReady()
	}
	runtimeErr := supervisor.Wait(func() error {
		recorderErr := recorder.Close()
		playerErr := player.Close()
		playerClosed = true
		return errors.Join(wrapError("close audio recorder", recorderErr), wrapError("close audio player", playerErr))
	})
	if !errors.Is(runtimeErr, voiceclient.ErrConnectionLost) {
		runtimeErr = errors.Join(runtimeErr, wrapError("send disconnect", voiceclient.Disconnect(conn, sessionID)))
	}
	return runtimeErr
}

func shouldRenderConnectionTree(firstConnection, alreadyPrinted, topologyUnchanged bool) bool {
	if alreadyPrinted {
		return false
	}
	return firstConnection || !topologyUnchanged
}

func classifyNetworkLoopError(operation string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	if errors.Is(err, voiceclient.ErrConnectionLost) {
		return err
	}
	return fmt.Errorf("%w: %s: %w", voiceclient.ErrConnectionLost, operation, err)
}

func clientAudioConfig() audio.CodecConfig {
	return audio.CodecConfig{SampleRate: sampleRate, Channels: channels, SamplesPerFrame: samplesPerFrame}
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
