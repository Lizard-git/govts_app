package clientapp

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

func runSession(parent context.Context, conn *udp.ClientPacketConn, state *voiceclient.State, devices AudioDeviceSelection, audioDeviceChanges <-chan audioDeviceChange, name string, preference *channelPreference, commands <-chan voiceclient.Command, output, noticeOutput io.Writer, cancelApp context.CancelFunc, logger *log.Logger, firstConnection bool, onReady func()) (runErr error) {
	output = &lockedOutput{writer: output}
	noticeOutput = &lockedOutput{writer: noticeOutput}
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
	player, err := audio.NewSwitchablePlayer(codecConfig, devices.PlaybackID)
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
	supervisor.Go(func(ctx context.Context) error { return voiceclient.ConsoleStateLoop(ctx, state, noticeOutput) })
	joinedSignal := make(chan struct{}, 1)
	if commands != nil {
		supervisor.Go(func(ctx context.Context) error {
			return voiceclient.SessionCommandLoop(ctx, conn, state, commands, output, cancelApp, func(locator voiceclient.ChannelLocator) {
				preference.Set(locator)
				select {
				case joinedSignal <- struct{}{}:
				default:
				}
			})
		})
	}

	finish := func(cause error) error {
		shutdownErr := supervisor.Shutdown(func() error {
			closeErr := player.Close()
			playerClosed = true
			return wrapError("close audio player", closeErr)
		})
		return errors.Join(cause, shutdownErr)
	}
	logger.Printf("client connected: id=%d name=%s", sessionID, name)
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
			logger.Printf("cannot restore channel: %v", resolveErr)
		}
	} else {
		id, resolveErr := voiceclient.ResolveChannel(snapshot, selector)
		if resolveErr == nil {
			channelID = id
		} else {
			logger.Printf("cannot resolve initial channel: %v", resolveErr)
		}
	}
	if channelID == 0 && len(snapshot.Channels) > 0 {
		channelID = snapshot.Channels[0].ID
		logger.Printf("joining first available channel: id=%d name=%q", channelID, snapshot.Channels[0].Name)
	}
	if channelID != 0 {
		if err := voiceclient.JoinChannel(ctx, conn, state, channelID); err != nil {
			logger.Printf("cannot join channel: %v", err)
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
		if commands != nil {
			select {
			case <-ctx.Done():
				return finish(nil)
			case <-joinedSignal:
				channelID = state.ChannelID()
			}
		} else {
			return finish(errors.New("server has no channel available to join"))
		}
	}
	currentSnapshot := state.Snapshot()
	activeProfile, err := channelAudioProfile(currentSnapshot, state.ChannelID())
	if err != nil {
		return finish(err)
	}
	if err := encoder.Configure(activeProfile); err != nil {
		return finish(fmt.Errorf("configure Opus encoder for channel %d: %w", state.ChannelID(), err))
	}
	topologyUnchanged := voiceclient.SameChannelTopology(oldSnapshot, currentSnapshot)
	if shouldRenderConnectionTree(firstConnection, treePrinted, topologyUnchanged) {
		if !firstConnection {
			logger.Printf("reconnected; channel structure changed")
		}
		_ = voiceclient.RenderServerTree(output, currentSnapshot, sessionID, state.ChannelID(), false)
	} else if !firstConnection {
		logger.Printf("reconnected; channel restored: id=%d", state.ChannelID())
	}
	state.SetConnectionStatus(voiceclient.ConnectionConnected)
	recorder, err := audio.NewSwitchableRecorder(codecConfig, devices.CaptureID)
	if err != nil {
		return finish(fmt.Errorf("create audio recorder: %w", err))
	}
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.EncodeLoopWithPipeline(ctx, encoder, filter, detector, gate, pcmCh, audioCh, state)
	})
	supervisor.Go(func(ctx context.Context) error {
		return voiceclient.RecordLoop(ctx, recorder, pcmCh, codecConfig.SamplesPerFrame, state.Audio)
	})
	supervisor.Go(func(ctx context.Context) error {
		return networkLoop("voice send", func() error { return voiceclient.SendLoop(ctx, conn, sessionID, audioCh, state.Audio) })
	})
	supervisor.Go(func(ctx context.Context) error {
		changed, unsubscribe := state.Subscribe(ctx)
		defer unsubscribe()
		channelID := state.ChannelID()
		profile := activeProfile
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
				nextChannelID := state.ChannelID()
				nextProfile, profileErr := channelAudioProfile(state.Snapshot(), nextChannelID)
				if profileErr != nil || (nextChannelID == channelID && nextProfile == profile) {
					continue
				}
				if err := encoder.Configure(nextProfile); err != nil {
					return fmt.Errorf("configure Opus encoder for channel %d: %w", nextChannelID, err)
				}
				channelID, profile = nextChannelID, nextProfile
			}
		}
	})
	supervisor.Go(func(ctx context.Context) error {
		current := devices
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case request := <-audioDeviceChanges:
				var changeErr error
				if request.selection.CaptureID != current.CaptureID {
					changeErr = recorder.Switch(request.selection.CaptureID)
				}
				if changeErr == nil && request.selection.PlaybackID != current.PlaybackID {
					changeErr = player.Switch(request.selection.PlaybackID)
				}
				if changeErr == nil {
					current = request.selection
				}
				request.done <- changeErr
			}
		}
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
	profile := domain.DefaultAudioProfile()

	return audio.CodecConfig{
		SampleRate:      int(profile.SampleRate),
		Channels:        int(profile.Channels),
		SamplesPerFrame: int(profile.SampleRate) * int(profile.FrameDurationMS) / 1000,
		Bitrate:         int(profile.Bitrate),
		Application:     profile.Application,
	}
}

func channelAudioProfile(snapshot domain.ServerSnapshot, channelID domain.ChannelID) (domain.AudioProfile, error) {
	for _, channel := range snapshot.Channels {
		if channel.ID == channelID {
			if err := domain.ValidateAudioProfile(channel.Audio); err != nil {
				return domain.AudioProfile{}, fmt.Errorf("channel %d audio profile: %w", channelID, err)
			}
			return channel.Audio, nil
		}
	}
	return domain.AudioProfile{}, fmt.Errorf("channel %d is missing from server snapshot", channelID)
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
