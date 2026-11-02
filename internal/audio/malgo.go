package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
)

// captureRateWindow is how often the recorder compares delivered samples with
// wall-clock time. Only windows with drops or a rate off by more than
// captureRateTolerance are logged, so a healthy device stays silent. A window
// longer than captureRateIdle means nobody was reading; it is discarded.
const (
	captureRateWindow    = 5 * time.Second
	captureRateTolerance = 0.05
	captureRateIdle      = 3 * captureRateWindow
	captureQueueChunks   = 8
)

type MalgoRecorder struct {
	config    CodecConfig
	context   *malgo.AllocatedContext
	device    *malgo.Device
	exclusive bool

	dataCh  chan []byte
	pending []byte

	// Written by the device callback, read and reset by Read.
	callbackFrames atomic.Uint64
	droppedChunks  atomic.Uint64
	rateStart      time.Time

	closeOnce sync.Once
	closeErr  error
}

type captureRateReport struct {
	framesPerSecond float64
	dropped         uint64
	window          time.Duration
}

func newMalgoRecorder(config CodecConfig) *MalgoRecorder {
	return &MalgoRecorder{config: config, dataCh: make(chan []byte, captureQueueChunks)}
}

func NewMalgoRecorderForDevice(config CodecConfig, deviceID string) (*MalgoRecorder, error) {
	recorder := newMalgoRecorder(config)
	callbacks := malgo.DeviceCallbacks{
		Data: func(
			output []byte,
			input []byte,
			frameCount uint32,
		) {
			recorder.push(input, frameCount)
		},
	}

	context, device, exclusive, err := openMalgoStream(malgo.Capture, config, deviceID, callbacks)
	if err != nil {
		return nil, err
	}
	recorder.context = context
	recorder.device = device
	recorder.exclusive = exclusive
	return recorder, nil
}

// Exclusive reports whether the recorder holds its endpoint in WASAPI
// exclusive mode, which blocks any other stream on the same device.
func (r *MalgoRecorder) Exclusive() bool {
	return r.exclusive
}

// push runs on the device thread and must not block.
func (r *MalgoRecorder) push(input []byte, frameCount uint32) {
	if len(input) == 0 {
		return
	}
	r.callbackFrames.Add(uint64(frameCount))

	chunk := make([]byte, len(input))
	copy(chunk, input)

	select {
	case r.dataCh <- chunk:
	default:
		// если consumer не успевает — дропаем старый real-time chunk
		r.droppedChunks.Add(1)
	}
}

func (r *MalgoRecorder) Read(samples []int16) (int, error) {
	if report, ok := r.checkCaptureRate(time.Now()); ok {
		log.Printf("capture rate anomaly: frames_per_s=%.0f expected=%d dropped_chunks=%d window=%s", report.framesPerSecond, r.config.SampleRate, report.dropped, report.window.Round(time.Millisecond))
	}
	written := 0

	for written < len(samples) {
		if len(r.pending) < 2 {
			chunk, ok := <-r.dataCh
			if !ok {
				return written, io.EOF
			}

			r.pending = chunk
		}

		availableSamples := len(r.pending) / 2
		neededSamples := len(samples) - written

		count := min(availableSamples, neededSamples)

		for i := 0; i < count; i++ {
			samples[written+i] = int16(binary.LittleEndian.Uint16(
				r.pending[i*2 : i*2+2],
			))
		}

		consumedBytes := count * 2
		r.pending = r.pending[consumedBytes:]
		written += count
	}

	return written, nil
}

// checkCaptureRate logs when the device delivers noticeably more or fewer
// frames than real time, or when the consumer could not keep up. A surplus
// sounds slowed down and later overflows the listener's jitter queue; a
// deficit or drops show up as concealment with no network loss.
// It returns a report only for a finished window that is anomalous.
func (r *MalgoRecorder) checkCaptureRate(now time.Time) (captureRateReport, bool) {
	if r.rateStart.IsZero() {
		r.rateStart = now
		r.callbackFrames.Store(0)
		r.droppedChunks.Store(0)
		return captureRateReport{}, false
	}
	elapsed := now.Sub(r.rateStart)
	if elapsed < captureRateWindow {
		return captureRateReport{}, false
	}
	frames := r.callbackFrames.Swap(0)
	dropped := r.droppedChunks.Swap(0)
	r.rateStart = now
	expected := float64(r.config.SampleRate)
	if expected <= 0 || elapsed > captureRateIdle {
		return captureRateReport{}, false
	}
	report := captureRateReport{framesPerSecond: float64(frames) / elapsed.Seconds(), dropped: dropped, window: elapsed}
	deviation := report.framesPerSecond/expected - 1
	return report, dropped > 0 || deviation > captureRateTolerance || deviation < -captureRateTolerance
}

func (r *MalgoRecorder) Close() error {
	r.closeOnce.Do(func() {
		var stopErr error
		var contextErr error

		if r.device != nil {
			if err := r.device.Stop(); err != nil {
				stopErr = fmt.Errorf("stop capture device: %w", err)
			}
			// Uninit must run even when Stop fails. It also ensures that the
			// callback cannot send to dataCh after the channel is closed.
			r.device.Uninit()
		}

		if r.dataCh != nil {
			close(r.dataCh)
		}

		if r.context != nil {
			if err := r.context.Uninit(); err != nil {
				contextErr = fmt.Errorf("uninitialize audio context: %w", err)
			}
			r.context.Free()
		}

		r.closeErr = errors.Join(stopErr, contextErr)
	})

	return r.closeErr
}
