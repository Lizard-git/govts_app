package rnnoise

import (
	"errors"
	"testing"
)

func TestProcessorAcceptsClientFrame(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	samples := make([]int16, FrameSize*2)
	analysis, err := p.Process(samples)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.VoiceProbabilityAvailable || analysis.VoiceProbability < 0 || analysis.VoiceProbability > 1 {
		t.Fatalf("invalid RNNoise VAD analysis: %+v", analysis)
	}
	if !analysis.VoiceDetectedAvailable {
		t.Fatalf("RNNoise did not classify VAD result: %+v", analysis)
	}
}

type testSettings struct {
	enabled bool
	gate    float32
}

func (s *testSettings) RNNoiseSnapshot() (bool, float32) { return s.enabled, s.gate }

func TestProcessorOwnsEnableAndGatePolicy(t *testing.T) {
	settings := &testSettings{enabled: false, gate: 0.5}
	p, err := New(settings)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	samples := make([]int16, FrameSize)
	analysis, err := p.Process(samples)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.VoiceDetectedAvailable {
		t.Fatal("disabled RNNoise returned VAD classification")
	}
	settings.enabled = true
	analysis, err = p.Process(samples)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.VoiceDetectedAvailable {
		t.Fatal("enabled RNNoise did not return VAD classification")
	}
}

func TestProcessorRejectsPartialFrameAndUseAfterClose(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Process(make([]int16, FrameSize+1)); err == nil {
		t.Fatal("partial RNNoise frame was accepted")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Process(make([]int16, FrameSize)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Process() after Close = %v, want %v", err, ErrClosed)
	}
}
