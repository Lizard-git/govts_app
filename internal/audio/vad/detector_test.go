package vad

import (
	"errors"
	"math"
	"testing"
)

func TestLevelDBFS(t *testing.T) {
	for _, samples := range [][]int16{nil, {}, {0, 0}} {
		if got := LevelDBFS(samples); got != SilenceDBFS {
			t.Fatalf("LevelDBFS(%v) = %g, want %g", samples, got, SilenceDBFS)
		}
	}
	if got := LevelDBFS([]int16{32767, -32768}); got < -0.01 || got > 0 {
		t.Fatalf("full-scale level = %g", got)
	}
	want := float32(20 * math.Log10(600.0/32768.0))
	if got := LevelDBFS([]int16{600, -600}); math.Abs(float64(got-want)) > 0.001 {
		t.Fatalf("level = %g, want %g", got, want)
	}
}

func TestWebRTCDetectorConfigLifecycleAndSilence(t *testing.T) {
	for _, config := range []WebRTCConfig{
		{SampleRate: 44100, Channels: 1, SamplesPerFrame: 882, Aggressiveness: ModeAggressive},
		{SampleRate: 48000, Channels: 2, SamplesPerFrame: 960, Aggressiveness: ModeAggressive},
		{SampleRate: 48000, Channels: 1, SamplesPerFrame: 100, Aggressiveness: ModeAggressive},
		{SampleRate: 48000, Channels: 1, SamplesPerFrame: 960, Aggressiveness: 4},
	} {
		if _, err := NewWebRTC(config); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
	detector, err := NewWebRTC(WebRTCConfig{SampleRate: 48000, Channels: 1, SamplesPerFrame: 960, Aggressiveness: ModeAggressive})
	if err != nil {
		t.Fatal(err)
	}
	result, err := detector.Analyze(make([]int16, 960))
	if err != nil {
		t.Fatal(err)
	}
	if result.Speech || result.ProbabilityAvailable || result.LevelDBFS != SilenceDBFS {
		t.Fatalf("silence result = %+v", result)
	}
	if _, err := detector.Analyze(make([]int16, 480)); err == nil {
		t.Fatal("invalid frame accepted")
	}
	if err := detector.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := detector.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := detector.Analyze(make([]int16, 960)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Analyze after Close = %v", err)
	}
}

func TestWebRTCDetectorRecognizesDeterministicSpeechLikeFrame(t *testing.T) {
	detector, err := NewWebRTC(WebRTCConfig{SampleRate: 48000, Channels: 1, SamplesPerFrame: 960, Aggressiveness: ModeAggressive})
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]int16, 960)
	for i := range frame {
		frame[i] = int16((i%160)*200 - 16000)
	}
	result, err := detector.Analyze(frame)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Speech || result.LevelDBFS >= 0 || result.LevelDBFS <= SilenceDBFS {
		t.Fatalf("speech-like result = %+v", result)
	}
}

func BenchmarkWebRTCDetector48k20ms(b *testing.B) {
	detector, err := NewWebRTC(WebRTCConfig{SampleRate: 48000, Channels: 1, SamplesPerFrame: 960, Aggressiveness: ModeAggressive})
	if err != nil {
		b.Fatal(err)
	}
	frame := make([]int16, 960)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := detector.Analyze(frame); err != nil {
			b.Fatal(err)
		}
	}
}
