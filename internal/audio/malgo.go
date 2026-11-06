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
	// captureQueueChunksMax is the channel capacity. An exclusive stream
	// delivers a whole buffer at once, possibly as one chunk per 20 ms period,
	// so its limit holds two buffers of the largest size plus slack.
	captureQueueChunksMax = 2*ExclusiveCaptureBufferMaxMS/ExclusiveCaptureBufferStepMS + 4
	// captureBurstGap separates two device deliveries: chunks of one delivery
	// arrive back to back, deliveries are at least a period apart.
	captureBurstGap = 2 * time.Millisecond
)

type MalgoRecorder struct {
	config      CodecConfig
	context     *malgo.AllocatedContext
	device      *malgo.Device
	exclusiveMS int

	dataCh  chan []byte
	pending []byte
	// queueLimit is how many chunks dataCh may hold; see captureQueueChunksMax.
	queueLimit atomic.Int32
	// queuedFrames counts frames in dataCh, so pacing knows the backlog.
	queuedFrames atomic.Int64

	// paceNext is when Read may return the next frame of an exclusive burst.
	paceNext time.Time

	// Written by the device callback, read and reset by Read.
	callbackFrames atomic.Uint64
	droppedChunks  atomic.Uint64
	rateStart      time.Time

	// Device-thread state of the current delivery; see observeDelivery.
	burstStart, lastPush time.Time
	burstFrames          uint64
	// Adaptive buffer window, written by push and swapped by adaptBuffer.
	adaptFrames   atomic.Uint64
	adaptDropped  atomic.Uint64
	maxLatenessNS atomic.Int64
	adaptStart    time.Time

	closeOnce sync.Once
	closeErr  error
}

type captureRateReport struct {
	framesPerSecond float64
	dropped         uint64
	window          time.Duration
}

func newMalgoRecorder(config CodecConfig) *MalgoRecorder {
	r := &MalgoRecorder{config: config, dataCh: make(chan []byte, captureQueueChunksMax)}
	r.queueLimit.Store(captureQueueChunks)
	return r
}

// exclusiveQueueChunks holds two buffers of 20 ms chunks plus slack.
func exclusiveQueueChunks(bufferMS int) int32 {
	return int32(min(captureQueueChunksMax, 2*bufferMS/ExclusiveCaptureBufferStepMS+4))
}

func NewMalgoRecorderForDevice(config CodecConfig, deviceID string) (*MalgoRecorder, error) {
	recorder := newMalgoRecorder(config)
	// The stream starts inside openMalgoStream, before it is known to be
	// exclusive, so its first burst must fit.
	recorder.queueLimit.Store(captureQueueChunksMax)
	callbacks := malgo.DeviceCallbacks{
		Data: func(
			output []byte,
			input []byte,
			frameCount uint32,
		) {
			recorder.push(input, frameCount)
		},
	}

	context, device, exclusiveMS, err := openMalgoStream(malgo.Capture, config, deviceID, callbacks)
	if err != nil {
		return nil, err
	}
	recorder.context = context
	recorder.device = device
	recorder.exclusiveMS = exclusiveMS
	if exclusiveMS > 0 {
		recorder.queueLimit.Store(exclusiveQueueChunks(exclusiveMS))
	} else {
		recorder.queueLimit.Store(captureQueueChunks)
	}
	return recorder, nil
}

// Exclusive reports whether the recorder holds its endpoint in WASAPI
// exclusive mode, which blocks any other stream on the same device.
func (r *MalgoRecorder) Exclusive() bool {
	return r.exclusiveMS > 0
}

// ExclusiveBufferMS is the buffer the exclusive stream opened with, or 0.
func (r *MalgoRecorder) ExclusiveBufferMS() int {
	return r.exclusiveMS
}

// push runs on the device thread and must not block.
func (r *MalgoRecorder) push(input []byte, frameCount uint32) {
	if len(input) == 0 {
		return
	}
	r.callbackFrames.Add(uint64(frameCount))
	r.adaptFrames.Add(uint64(frameCount))
	r.observeDelivery(time.Now(), frameCount)

	chunk := make([]byte, len(input))
	copy(chunk, input)

	// The consumer cannot keep up: drop the newest real-time chunk.
	if len(r.dataCh) >= int(r.queueLimit.Load()) {
		r.dropChunk()
		return
	}
	select {
	case r.dataCh <- chunk:
		r.queuedFrames.Add(int64(frameCount))
	default:
		r.dropChunk()
	}
}

func (r *MalgoRecorder) dropChunk() {
	r.droppedChunks.Add(1)
	r.adaptDropped.Add(1)
}

// observeDelivery runs on the device thread. A delivery is a run of chunks
// arriving back to back. The device signals the next one once the previous
// delivery's worth of audio has accumulated, so a delivery that starts later
// than that shows how late the capture thread woke.
func (r *MalgoRecorder) observeDelivery(now time.Time, frameCount uint32) {
	if !r.lastPush.IsZero() && now.Sub(r.lastPush) <= captureBurstGap {
		r.lastPush = now
		r.burstFrames += uint64(frameCount)
		return
	}
	if !r.burstStart.IsZero() && r.config.SampleRate > 0 {
		expected := time.Duration(r.burstFrames) * time.Second / time.Duration(r.config.SampleRate)
		if lateness := now.Sub(r.burstStart) - expected; lateness > 0 {
			for {
				old := r.maxLatenessNS.Load()
				if int64(lateness) <= old || r.maxLatenessNS.CompareAndSwap(old, int64(lateness)) {
					break
				}
			}
		}
	}
	r.burstStart, r.lastPush, r.burstFrames = now, now, uint64(frameCount)
}

// adaptBuffer feeds the adaptive exclusive buffer once per captureRateWindow.
// A changed buffer takes effect when SwitchableRecorder reopens the stream.
func (r *MalgoRecorder) adaptBuffer(now time.Time) {
	if r.exclusiveMS == 0 || r.config.SampleRate <= 0 {
		return
	}
	if r.adaptStart.IsZero() {
		// The first window starts after the device start-up.
		r.adaptStart = now
		r.adaptFrames.Store(0)
		r.adaptDropped.Store(0)
		r.maxLatenessNS.Store(0)
		return
	}
	elapsed := now.Sub(r.adaptStart)
	if elapsed < captureRateWindow {
		return
	}
	frames := r.adaptFrames.Swap(0)
	dropped := r.adaptDropped.Swap(0)
	lateness := time.Duration(r.maxLatenessNS.Swap(0))
	r.adaptStart = now
	if elapsed > captureRateIdle {
		return
	}
	// Delivery boundaries shift a window by up to one buffer; a larger
	// shortfall than that plus 1% clock drift is audio the device overwrote.
	rate := float64(r.config.SampleRate)
	expected := elapsed.Seconds() * rate
	slack := float64(r.exclusiveMS+ExclusiveCaptureBufferStepMS) / 1000 * rate
	lost := dropped > 0 || float64(frames)+slack < expected*0.99
	adaptExclusiveCaptureBuffer(r.exclusiveMS, lateness, lost, now)
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
			r.queuedFrames.Add(-int64(r.chunkFrames(len(chunk))))

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
	now := time.Now()
	r.adaptBuffer(now)
	r.pace(written, now)

	return written, nil
}

func (r *MalgoRecorder) chunkFrames(bytes int) int {
	return bytes / 2 / max(1, r.config.Channels)
}

// pace spreads an exclusive stream's buffer-sized bursts over real time, so
// frames leave one per 20 ms instead of all at once and the listener does not
// drop the burst's tail as late. Latency stays about one buffer. When Read
// falls behind, or more than one buffer is queued because the device clock
// runs fast or the capture thread woke late, it returns at once and catches
// up instead of adding delay. A delivery split into one chunk per period is
// still one buffer, so it is paced.
func (r *MalgoRecorder) pace(samples int, now time.Time) {
	if r.exclusiveMS <= ExclusiveCaptureBufferStepMS || r.config.SampleRate <= 0 {
		return
	}
	frame := time.Duration(samples) * time.Second / time.Duration(r.config.SampleRate*max(1, r.config.Channels))
	backlog := r.queuedFrames.Load() + int64(r.chunkFrames(len(r.pending)))
	buffer := int64(r.exclusiveMS+ExclusiveCaptureBufferStepMS) * int64(r.config.SampleRate) / 1000
	if r.paceNext.IsZero() || now.Sub(r.paceNext) > frame || backlog > buffer {
		r.paceNext = now.Add(frame)
		return
	}
	if wait := r.paceNext.Sub(now); wait > 0 {
		time.Sleep(wait)
	}
	r.paceNext = r.paceNext.Add(frame)
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
