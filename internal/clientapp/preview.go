package clientapp

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"uniclog.io/sonoryx/internal/audio"
	"uniclog.io/sonoryx/internal/audio/voicegate"
	voiceclient "uniclog.io/sonoryx/internal/client"
)

// The microphone preview runs the capture pipeline (filter, VAD, voice gate)
// while no session is active, so the settings meter shows what would be
// transmitted before joining a server. Encoded output is never produced.
type micPreview struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// previewSink stands in for the Opus encoder; the preview never sends.
type previewSink struct{}

func (previewSink) Encode([]int16) ([]byte, error) { return nil, nil }

// SetMicrophonePreview requests the preview; it only runs while disconnected
// and stops for the duration of a connection. Disabling it also stops the
// monitor, so leaving the microphone settings always resumes transmission.
func (a *App) SetMicrophonePreview(enabled bool) {
	a.previewMu.Lock()
	defer a.previewMu.Unlock()
	a.previewWanted = enabled
	if !enabled {
		a.stopMonitorLocked()
	}
	a.syncPreviewLocked()
}

// SetMicrophoneMonitor plays the frames that pass the voice gate on the
// playback device. Before joining a server the preview supplies them; in a
// session the capture pipeline does, and transmission is blocked meanwhile.
func (a *App) SetMicrophoneMonitor(enabled bool) error {
	a.previewMu.Lock()
	defer a.previewMu.Unlock()
	if !enabled {
		a.stopMonitorLocked()
		return nil
	}
	if a.monitor != nil {
		return nil
	}
	return a.startMonitorLocked()
}

func (a *App) startMonitorLocked() error {
	// A separate stream: the session player belongs to MixLoop, and the
	// system mixer combines both on the same device.
	player, err := audio.NewSwitchablePlayer(clientAudioConfig(), a.AudioDeviceSelection().PlaybackID)
	if err != nil {
		return fmt.Errorf("create monitor player: %w", err)
	}
	a.monitor = player
	a.state.Audio.SetMonitor(func(samples []int16) { _ = player.Write(samples) })
	return nil
}

func (a *App) stopMonitorLocked() {
	if a.monitor == nil {
		return
	}
	a.state.Audio.SetMonitor(nil)
	if err := a.monitor.Close(); err != nil {
		a.logger.Printf("close monitor player: %v", err)
	}
	a.monitor = nil
}

func (a *App) stopMonitor() {
	a.previewMu.Lock()
	defer a.previewMu.Unlock()
	a.stopMonitorLocked()
}

func (a *App) syncPreview() {
	a.previewMu.Lock()
	defer a.previewMu.Unlock()
	a.syncPreviewLocked()
}

// restartPreview reopens devices after a selection change.
func (a *App) restartPreview(capture bool) {
	a.previewMu.Lock()
	defer a.previewMu.Unlock()
	if !capture {
		if a.monitor != nil {
			if err := a.monitor.Switch(a.AudioDeviceSelection().PlaybackID); err != nil {
				a.logger.Printf("switch monitor player: %v", err)
			}
		}
		return
	}
	a.stopPreviewLocked()
	a.syncPreviewLocked()
}

func (a *App) syncPreviewLocked() {
	a.mu.Lock()
	run := a.previewWanted && !a.active && !a.closed
	a.mu.Unlock()
	if !run {
		a.stopPreviewLocked()
		return
	}
	if a.preview != nil {
		return
	}
	ctx, cancel := context.WithCancel(a.lifetimeCtx)
	preview := &micPreview{cancel: cancel, done: make(chan struct{})}
	a.preview = preview
	devices := a.AudioDeviceSelection()
	go func() {
		defer close(preview.done)
		if err := a.runPreview(ctx, devices); err != nil && !errors.Is(err, context.Canceled) {
			a.logger.Printf("microphone preview: %v", err)
		}
	}()
}

func (a *App) stopPreviewLocked() {
	if a.preview == nil {
		return
	}
	a.preview.cancel()
	<-a.preview.done
	a.preview = nil
}

func (a *App) runPreview(ctx context.Context, devices AudioDeviceSelection) (runErr error) {
	codecConfig := clientAudioConfig()
	filter, err := newMicrophoneFilter(codecConfig, a.state.Audio)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, wrapError("close microphone filter", filter.Close())) }()
	detector, err := newMicrophoneVAD(codecConfig)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, wrapError("close microphone VAD", detector.Close())) }()
	gate, err := voicegate.New(a.state.Audio.VADSnapshot().GateConfig())
	if err != nil {
		return fmt.Errorf("create microphone voice gate: %w", err)
	}
	recorder, err := audio.NewSwitchableRecorder(codecConfig, devices.CaptureID)
	if recorder == nil {
		return fmt.Errorf("create audio recorder: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pcmCh := make(chan audio.PCMFrame)
	audioCh := make(chan audio.Frame)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		errs <- voiceclient.EncodeLoopWithPipeline(ctx, previewSink{}, filter, detector, gate, pcmCh, audioCh, a.state)
	}()
	go func() {
		defer wg.Done()
		errs <- voiceclient.RecordLoop(ctx, recorder, pcmCh, codecConfig.SamplesPerFrame, a.state.Audio)
	}()
	go func() {
		defer wg.Done()
		for range audioCh {
		}
	}()
	select {
	case <-ctx.Done():
	case runErr = <-errs:
	}
	cancel()
	// Read blocks inside the device until the recorder is closed.
	closeErr := recorder.Close()
	wg.Wait()
	return errors.Join(runErr, wrapError("close audio recorder", closeErr))
}
