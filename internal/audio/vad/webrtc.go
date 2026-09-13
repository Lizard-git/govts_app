package vad

import (
	"fmt"

	webrtcvad "github.com/rolandhe/go-vad"
)

const (
	ModeQuality        = 0
	ModeLowBitrate     = 1
	ModeAggressive     = 2
	ModeVeryAggressive = 3
)

type WebRTCConfig struct {
	SampleRate      int
	Channels        int
	SamplesPerFrame int
	Aggressiveness  int
}

type WebRTCDetector struct {
	engine *webrtcvad.VAD
	config WebRTCConfig
	closed bool
}

var _ Detector = (*WebRTCDetector)(nil)

func NewWebRTC(config WebRTCConfig) (*WebRTCDetector, error) {
	if err := validateWebRTCConfig(config); err != nil {
		return nil, err
	}
	detector := &WebRTCDetector{engine: webrtcvad.New(), config: config}
	if err := detector.configure(); err != nil {
		return nil, err
	}
	return detector, nil
}

func validateWebRTCConfig(config WebRTCConfig) error {
	if config.Channels != 1 {
		return fmt.Errorf("WebRTC VAD requires mono audio, got %d channels", config.Channels)
	}
	validRate := config.SampleRate == 8000 || config.SampleRate == 16000 || config.SampleRate == 32000 || config.SampleRate == 48000
	if !validRate {
		return fmt.Errorf("WebRTC VAD does not support sample rate %d", config.SampleRate)
	}
	validFrame := false
	for _, milliseconds := range [...]int{10, 20, 30} {
		if config.SamplesPerFrame == config.SampleRate*milliseconds/1000 {
			validFrame = true
			break
		}
	}
	if !validFrame {
		return fmt.Errorf("WebRTC VAD requires a 10, 20, or 30 ms frame, got %d samples at %d Hz", config.SamplesPerFrame, config.SampleRate)
	}
	if config.Aggressiveness < ModeQuality || config.Aggressiveness > ModeVeryAggressive {
		return fmt.Errorf("WebRTC VAD aggressiveness must be between 0 and 3, got %d", config.Aggressiveness)
	}
	return nil
}

func (d *WebRTCDetector) configure() error {
	if err := d.engine.SetSampleRate(webrtcvad.SampleRate(d.config.SampleRate)); err != nil {
		return fmt.Errorf("configure WebRTC VAD sample rate: %w", err)
	}
	if err := d.engine.SetMode(webrtcvad.Mode(d.config.Aggressiveness)); err != nil {
		return fmt.Errorf("configure WebRTC VAD aggressiveness: %w", err)
	}
	return nil
}

func (d *WebRTCDetector) Analyze(samples []int16) (Result, error) {
	if d.closed {
		return Result{}, ErrClosed
	}
	if len(samples) != d.config.SamplesPerFrame {
		return Result{}, fmt.Errorf("WebRTC VAD expected %d samples, got %d", d.config.SamplesPerFrame, len(samples))
	}
	decision, err := d.engine.Process(samples)
	if err != nil {
		return Result{}, fmt.Errorf("analyze WebRTC VAD frame: %w", err)
	}
	return Result{Speech: decision == webrtcvad.ResultVoice, LevelDBFS: LevelDBFS(samples)}, nil
}

func (d *WebRTCDetector) Reset() error {
	if d.closed {
		return ErrClosed
	}
	d.engine.Reset()
	return d.configure()
}

func (d *WebRTCDetector) Close() error {
	d.closed = true
	d.engine = nil
	return nil
}
