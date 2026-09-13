package rnnoise

import (
	"errors"
	"fmt"
	"math"

	lib "github.com/MarcosTypeAP/go-rnnoise"

	"example.com/go-voice-mvp/internal/audio"
)

const (
	SampleRate = 48000
	FrameSize  = lib.FrameSize
)

var ErrClosed = errors.New("RNNoise processor is closed")

// Processor applies the built-in RNNoise model to one mono 48 kHz PCM stream.
// It must be used sequentially because the model keeps history between frames.
type Processor struct {
	state      *lib.DenoiseState
	frame      []float32
	settings   Settings
	needsReset bool
}

type Settings interface {
	RNNoiseSnapshot() (enabled bool, gate float32)
}

func New(settings ...Settings) (*Processor, error) {
	p := &Processor{frame: make([]float32, FrameSize)}
	if len(settings) > 0 {
		p.settings = settings[0]
	}
	if err := p.Reset(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Processor) Reset() error {
	state, err := lib.New(nil)
	if err != nil {
		return fmt.Errorf("initialize RNNoise: %w", err)
	}
	p.state = state
	p.needsReset = false
	if len(p.frame) != FrameSize {
		p.frame = make([]float32, FrameSize)
	}
	return nil
}

func (p *Processor) Process(samples []int16) (audio.PCMAnalysis, error) {
	if p.state == nil {
		return audio.PCMAnalysis{}, ErrClosed
	}
	enabled, gate := true, float32(0.5)
	if p.settings != nil {
		enabled, gate = p.settings.RNNoiseSnapshot()
	}
	if !enabled {
		p.needsReset = true
		return audio.PCMAnalysis{}, nil
	}
	if p.needsReset {
		if err := p.Reset(); err != nil {
			return audio.PCMAnalysis{}, err
		}
	}
	if len(samples)%FrameSize != 0 {
		return audio.PCMAnalysis{}, fmt.Errorf("RNNoise requires blocks divisible by %d samples, got %d", FrameSize, len(samples))
	}

	var probability float32
	for offset := 0; offset < len(samples); offset += FrameSize {
		for i := range FrameSize {
			p.frame[i] = float32(samples[offset+i])
		}
		vad := p.state.ProcessFrame(p.frame, p.frame)
		if vad > probability {
			probability = vad
		}
		for i := range FrameSize {
			samples[offset+i] = pcm16(p.frame[i])
		}
	}
	return audio.PCMAnalysis{
		VoiceProbability:          probability,
		VoiceProbabilityAvailable: true,
		VoiceDetected:             probability >= gate,
		VoiceDetectedAvailable:    true,
	}, nil
}

func (p *Processor) Close() error {
	p.state = nil
	p.frame = nil
	return nil
}

func pcm16(sample float32) int16 {
	if math.IsNaN(float64(sample)) {
		return 0
	}
	if sample > math.MaxInt16 {
		return math.MaxInt16
	}
	if sample < math.MinInt16 {
		return math.MinInt16
	}
	return int16(math.Round(float64(sample)))
}
