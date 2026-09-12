package client

import (
	"context"
	"time"

	"example.com/go-voice-mvp/internal/audio"
)

// At the fixed 20 ms audio profile, keep at most 100 ms per sender.
const maxMixerFramesPerSender = 5

type pcmMixer struct {
	bySender      map[uint64][]audio.MediaPCMFrame
	droppedFrames uint64
}

func newPCMMixer() *pcmMixer {
	return &pcmMixer{
		bySender: make(map[uint64][]audio.MediaPCMFrame),
	}
}

func (m *pcmMixer) Push(frame audio.MediaPCMFrame) {
	queue := m.bySender[frame.SenderID]
	if len(queue) == maxMixerFramesPerSender {
		copy(queue, queue[1:])
		queue[len(queue)-1] = frame
		m.droppedFrames++
	} else {
		queue = append(queue, frame)
	}
	m.bySender[frame.SenderID] = queue
}

func (m *pcmMixer) Mix() (audio.PCMFrame, bool) {
	frames := make([]audio.MediaPCMFrame, 0, len(m.bySender))
	for senderID, queue := range m.bySender {
		if len(queue) == 0 {
			delete(m.bySender, senderID)
			continue
		}

		frames = append(frames, queue[0])
		queue[0] = audio.MediaPCMFrame{} // Release consumed sample storage.
		if len(queue) == 1 {
			delete(m.bySender, senderID)
		} else {
			m.bySender[senderID] = queue[1:]
		}
	}

	if len(frames) == 0 {
		return audio.PCMFrame{}, false
	}
	return mixPCMFrames(frames), true
}

func mixPCMFrames(frames []audio.MediaPCMFrame) audio.PCMFrame {
	sampleCount := 0
	duration := time.Duration(0)
	for _, frame := range frames {
		if len(frame.Samples) > sampleCount {
			sampleCount = len(frame.Samples)
		}
		if frame.Duration > duration {
			duration = frame.Duration
		}
	}

	sums := make([]int64, sampleCount)
	for _, frame := range frames {
		for i, sample := range frame.Samples {
			sums[i] += int64(sample)
		}
	}

	samples := make([]int16, sampleCount)
	for i, sum := range sums {
		samples[i] = clampInt16(sum)
	}
	return audio.PCMFrame{Samples: samples, Duration: duration}
}

func clampInt16(value int64) int16 {
	const (
		minInt16 = -1 << 15
		maxInt16 = 1<<15 - 1
	)

	if value < minInt16 {
		return minInt16
	}
	if value > maxInt16 {
		return maxInt16
	}
	return int16(value)
}

func MixLoop(
	ctx context.Context,
	decodedCh <-chan audio.MediaPCMFrame,
	mixedCh chan<- audio.PCMFrame,
	interval time.Duration,
) error {
	defer close(mixedCh)
	if interval <= 0 {
		interval = frameDuration
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	mixer := newPCMMixer()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame, ok := <-decodedCh:
			if !ok {
				return drainMixer(ctx, mixer, mixedCh)
			}
			mixer.Push(frame)
		case <-ticker.C:
			frame, ok := mixer.Mix()
			if !ok {
				continue
			}
			if err := sendMixedFrame(ctx, mixedCh, frame); err != nil {
				return err
			}
		}
	}
}

func drainMixer(
	ctx context.Context,
	mixer *pcmMixer,
	mixedCh chan<- audio.PCMFrame,
) error {
	for {
		frame, ok := mixer.Mix()
		if !ok {
			return nil
		}
		if err := sendMixedFrame(ctx, mixedCh, frame); err != nil {
			return err
		}
	}
}

func sendMixedFrame(
	ctx context.Context,
	mixedCh chan<- audio.PCMFrame,
	frame audio.PCMFrame,
) error {
	select {
	case mixedCh <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
