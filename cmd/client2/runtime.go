package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"example.com/go-voice-mvp/internal/audio"
	audiornnoise "example.com/go-voice-mvp/internal/audio/rnnoise"
	audiovad "example.com/go-voice-mvp/internal/audio/vad"
	"example.com/go-voice-mvp/internal/audio/voicegate"
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
	output = &lockedOutput{writer: output}
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
	filter, err := newMicrophoneFilter(codecConfig, state.Audio)
	if err != nil {
		return err
	}
	defer func() {
		if err := filter.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close microphone filter: %w", err))
		}
	}()
	detector, err := newMicrophoneVAD(codecConfig)
	if err != nil {
		return err
	}
	defer func() {
		if err := detector.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close microphone VAD: %w", err))
		}
	}()
	gate, err := voicegate.New(state.Audio.VADSnapshot().GateConfig())
	if err != nil {
		return fmt.Errorf("create microphone voice gate: %w", err)
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
	detachPlayer, err := state.Audio.AttachPlayer(player)
	if err != nil {
		return err
	}
	defer detachPlayer()
	supervisor := newLoopSupervisor(parent)
	ctx := supervisor.Context()
	defer supervisor.Cancel()
	networkLoop := func(operation string, loop func() error) error {
		return classifyNetworkLoopError(operation, loop())
	}
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.DecodeLoop(ctx, func() (audio.Decoder, error) { return audio.NewOpusDecoder(codecConfig) }, orderedInCh, decodedCh, state)
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
	supervisor.Go(func(ctx context.Context) error { return voiceclient.PlaybackLoop(ctx, player, pcmOutCh, state.Audio) })
	supervisor.Go(func(ctx context.Context) error { return voiceclient.ControlLoop(ctx, state, controlCh) })
	supervisor.Go(func(ctx context.Context) error { return voiceclient.SpeakingLoop(ctx, state) })
	supervisor.Go(func(ctx context.Context) error { return voiceclient.ConsoleStateLoop(ctx, state, output) })
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
	syncer := &voiceclient.StateSyncer{Conn: conn, State: state}
	snapshot, err := syncer.Load(ctx)
	if err != nil {
		return finish(fmt.Errorf("%w: load server snapshot: %v", voiceclient.ErrConnectionLost, err))
	}
	supervisor.Go(syncer.Run)
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
			snapshot, err = syncer.Load(ctx)
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
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.EncodeLoopWithPipeline(ctx, encoder, filter, detector, gate, pcmCh, audioCh, state)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.RecordLoop(ctx, recorder, pcmCh, samplesPerFrame, state.Audio)
	})
	supervisor.Go(func(ctx context.Context) error {
		return networkLoop("voice send", func() error { return voiceclient.SendLoop(ctx, conn, sessionID, audioCh, state.Audio) })
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

func newMicrophoneFilter(config audio.CodecConfig, settings audiornnoise.Settings) (audio.PCMFilter, error) {
	if config.SampleRate != audiornnoise.SampleRate || config.Channels != 1 {
		return nil, fmt.Errorf("RNNoise requires 48000 Hz mono audio, got %d Hz with %d channels", config.SampleRate, config.Channels)
	}
	if config.SamplesPerFrame%audiornnoise.FrameSize != 0 {
		return nil, fmt.Errorf("RNNoise frame size %d does not divide client frame size %d", audiornnoise.FrameSize, config.SamplesPerFrame)
	}
	processor, err := audiornnoise.New(settings)
	if err != nil {
		return nil, fmt.Errorf("create RNNoise processor: %w", err)
	}
	return processor, nil
}

func newMicrophoneVAD(config audio.CodecConfig) (audiovad.Detector, error) {
	detector, err := audiovad.NewWebRTC(audiovad.WebRTCConfig{
		SampleRate:      config.SampleRate,
		Channels:        config.Channels,
		SamplesPerFrame: config.SamplesPerFrame,
		Aggressiveness:  audiovad.ModeAggressive,
	})
	if err != nil {
		return nil, fmt.Errorf("create WebRTC VAD: %w", err)
	}
	return detector, nil
}

type lockedOutput struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
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
