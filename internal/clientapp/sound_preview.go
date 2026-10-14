package clientapp

import (
	"context"
	"errors"
	"time"

	"uniclog.io/sonoryx/internal/audio"
	voiceclient "uniclog.io/sonoryx/internal/client"
)

func (a *App) PreviewSoundPack() error {
	a.soundPreviewMu.Lock()
	if a.soundPreviewCancel != nil {
		a.soundPreviewCancel()
	}
	ctx, cancel := context.WithCancel(a.lifetimeCtx)
	a.soundPreviewCancel = cancel
	a.soundPreviewMu.Unlock()

	a.soundPreviewRun.Lock()
	defer a.soundPreviewRun.Unlock()
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil
	}

	pack, volume := a.state.Audio.SoundSnapshot()
	if volume <= 0 {
		return nil
	}
	// The session player is already open. A second WASAPI stream on the same
	// endpoint, especially Remote Desktop audio, stalls for the whole open
	// attempt before the first sample.
	deafened, _ := a.state.Audio.PlaybackSnapshot()
	if a.sessionPlaying() && !deafened {
		return a.playPackThroughSession(ctx, pack)
	}
	player, err := audio.NewMalgoPlayer(clientAudioConfig(), a.AudioDeviceSelection().PlaybackID)
	if err != nil {
		deafened, _ := a.state.Audio.PlaybackSnapshot()
		if a.sessionPlaying() && !deafened {
			return a.playPackThroughSession(ctx, pack)
		}
		return err
	}
	defer func() { _ = player.Close() }()

	frame := clientAudioConfig()
	frameSamples := frame.SamplesPerFrame * frame.Channels
	if frameSamples <= 0 {
		frameSamples = 960
	}
	for _, kind := range voiceclient.PreviewSoundKinds() {
		samples := voiceclient.RenderNotificationPCM(pack, volume, kind)
		if err := writePreviewPCM(ctx, player, samples, frameSamples); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}

func (a *App) StopSoundPreview() {
	a.soundPreviewMu.Lock()
	defer a.soundPreviewMu.Unlock()
	if a.soundPreviewCancel != nil {
		a.soundPreviewCancel()
	}
}

func (a *App) sessionPlaying() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active && !a.closed && a.state.ConnectionStatus() == voiceclient.ConnectionConnected
}

func (a *App) playPackThroughSession(ctx context.Context, pack string) error {
	_, volume := a.state.Audio.SoundSnapshot()
	for _, kind := range voiceclient.PreviewSoundKinds() {
		samples := voiceclient.RenderNotificationPCM(pack, volume, kind)
		a.state.PlayNotification(kind)
		wait := time.Duration(len(samples)) * time.Second / 48000
		if wait < 20*time.Millisecond {
			wait = 20 * time.Millisecond
		}
		wait += 100 * time.Millisecond
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
	return nil
}

func writePreviewPCM(ctx context.Context, player *audio.MalgoPlayer, samples []int16, frameSamples int) error {
	if len(samples) == 0 {
		return nil
	}
	written := 0
	for primed := 0; primed < 3 && written < len(samples); primed++ {
		end := written + frameSamples
		if end > len(samples) {
			end = len(samples)
		}
		if err := player.Write(samples[written:end]); err != nil {
			return err
		}
		written = end
	}
	for written < len(samples) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
		end := written + frameSamples
		if end > len(samples) {
			end = len(samples)
		}
		if err := player.Write(samples[written:end]); err != nil {
			return err
		}
		written = end
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(60 * time.Millisecond):
		return nil
	}
}
