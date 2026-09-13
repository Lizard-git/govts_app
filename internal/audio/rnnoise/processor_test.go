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
	enabled bool
}

func (s *testSettings) RNNoiseEnabled() bool { return s.enabled }

func TestProcessorOwnsEnablePolicy(t *testing.T) {
	settings := &testSettings{enabled: false}
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
