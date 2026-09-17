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
	if err := p.Process(samples); err != nil {
		t.Fatal(err)
	}
}

type testSettings struct {
	enabled     bool
	sensitivity float32
}

func (s *testSettings) RNNoiseEnabled() bool        { return s.enabled }
func (s *testSettings) RNNoiseSensitivity() float32 { return s.sensitivity }

func TestProcessorOwnsEnablePolicy(t *testing.T) {
	settings := &testSettings{enabled: false, sensitivity: 1}
	p, err := New(settings)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	samples := make([]int16, FrameSize)
	if err := p.Process(samples); err != nil {
		t.Fatal(err)
	}
	settings.enabled = true
	if err := p.Process(samples); err != nil {
		t.Fatal(err)
	}
}

func TestZeroSensitivityPreservesInput(t *testing.T) {
	settings := &testSettings{enabled: true, sensitivity: 0}
	p, err := New(settings)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	samples := make([]int16, FrameSize)
	for i := range samples {
		samples[i] = int16(i%101 - 50)
	}
	want := append([]int16(nil), samples...)
	if err := p.Process(samples); err != nil {
		t.Fatal(err)
	}
	for i := range samples {
		if samples[i] != want[i] {
			t.Fatalf("sample %d = %d, want %d", i, samples[i], want[i])
		}
	}
}

func TestProcessorRejectsPartialFrameAndUseAfterClose(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Process(make([]int16, FrameSize+1)); err == nil {
		t.Fatal("partial RNNoise frame was accepted")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Process(make([]int16, FrameSize)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Process() after Close = %v, want %v", err, ErrClosed)
	}
}
