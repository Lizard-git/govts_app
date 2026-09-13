package vad

import (
	"errors"
	"fmt"
	"math"
)

const SilenceDBFS float32 = -96

var ErrClosed = errors.New("VAD detector is closed")

type Result struct {
	Speech               bool
	Probability          float32
	ProbabilityAvailable bool
	LevelDBFS            float32
}

type Detector interface {
	Analyze(samples []int16) (Result, error)
	Reset() error
	Close() error
}

func LevelDBFS(samples []int16) float32 {
	if len(samples) == 0 {
		return SilenceDBFS
	}
	var sum float64
	for _, sample := range samples {
		value := float64(sample)
		sum += value * value
	}
	if sum == 0 {
		return SilenceDBFS
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	level := 20 * math.Log10(rms/32768)
	if math.IsNaN(level) || math.IsInf(level, -1) || level < float64(SilenceDBFS) {
		return SilenceDBFS
	}
	if level > 0 {
		return 0
	}
	return float32(level)
}

func ValidateProbability(value float32) error {
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || value < 0 || value > 1 {
		return fmt.Errorf("probability must be between 0 and 1, got %g", value)
	}
	return nil
}
