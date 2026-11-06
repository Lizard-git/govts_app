package audio

import (
	"log"
	"sync"
	"time"
)

// The WASAPI buffer of the exclusive capture fallback. It is also how late the
// capture thread may wake before the device overwrites unread audio, so a
// larger buffer survives a busier CPU at the cost of the same amount of
// latency. Shared-mode capture ignores it.
//
// By default the buffer is adaptive: it starts at ExclusiveCaptureBufferStartMS
// and the recorder moves it within ExclusiveCaptureBufferMinMS..MaxMS from the
// measured wake-up lateness of the capture thread. With adaptation off it is
// ExclusiveCaptureBufferFixedMS. Each change reopens the exclusive stream,
// which is a short gap in the voice, so changes stay rare.
const (
	ExclusiveCaptureBufferStepMS  = 20
	ExclusiveCaptureBufferMinMS   = 60
	ExclusiveCaptureBufferMaxMS   = 200
	ExclusiveCaptureBufferStartMS = 100
	ExclusiveCaptureBufferFixedMS = 180

	// A buffer of B ms tolerates a wake-up up to B ms late. The controller
	// keeps half of it as reserve: lateness above B/2 grows the buffer.
	exclusiveBufferReserveFactor = 2
	// The buffer shrinks one step only after this long without lateness that
	// the smaller buffer would not have covered with the same reserve.
	exclusiveBufferShrinkAfter = 10 * time.Minute
)

type exclusiveBufferState struct {
	mu       sync.Mutex
	adaptive bool
	ms       int
	// calmSince is when lateness last needed the current buffer.
	calmSince time.Time
}

var exclusiveBuffer = exclusiveBufferState{adaptive: true, ms: ExclusiveCaptureBufferStartMS}

// ExclusiveCaptureBufferMS is the buffer the next exclusive capture opens with.
func ExclusiveCaptureBufferMS() int {
	exclusiveBuffer.mu.Lock()
	defer exclusiveBuffer.mu.Unlock()
	return exclusiveBuffer.ms
}

// ExclusiveCaptureBufferAdaptive reports whether the recorder picks the buffer.
func ExclusiveCaptureBufferAdaptive() bool {
	exclusiveBuffer.mu.Lock()
	defer exclusiveBuffer.mu.Unlock()
	return exclusiveBuffer.adaptive
}

// SetExclusiveCaptureBufferAdaptive switches between the adaptive buffer,
// which restarts from ExclusiveCaptureBufferStartMS, and the fixed one. An open
// exclusive recorder reopens with the new value on its next read.
func SetExclusiveCaptureBufferAdaptive(adaptive bool) {
	exclusiveBuffer.mu.Lock()
	defer exclusiveBuffer.mu.Unlock()
	if exclusiveBuffer.adaptive == adaptive {
		return
	}
	exclusiveBuffer.adaptive = adaptive
	exclusiveBuffer.ms = ExclusiveCaptureBufferFixedMS
	if adaptive {
		exclusiveBuffer.ms = ExclusiveCaptureBufferStartMS
	}
	exclusiveBuffer.calmSince = time.Time{}
	log.Printf("exclusive capture buffer mode: adaptive=%t buffer_ms=%d", adaptive, exclusiveBuffer.ms)
}

// adaptExclusiveCaptureBuffer is called once per capture measurement window by
// the exclusive recorder that opened with openedMS. lateness is the largest
// wake-up delay of the capture thread in the window; lost reports audio the
// device delivered short of real time or chunks the queue dropped. It returns
// the new buffer when it changed; the recorder then reopens.
func adaptExclusiveCaptureBuffer(openedMS int, lateness time.Duration, lost bool, now time.Time) (int, bool) {
	exclusiveBuffer.mu.Lock()
	defer exclusiveBuffer.mu.Unlock()
	state := &exclusiveBuffer
	// A stale recorder (mode or value changed meanwhile) only reopens.
	if !state.adaptive || state.ms != openedMS {
		return 0, false
	}
	if state.calmSince.IsZero() {
		state.calmSince = now
	}
	needed := roundUpBufferStep(time.Duration(exclusiveBufferReserveFactor) * lateness)
	if lost {
		needed = max(needed, state.ms+ExclusiveCaptureBufferStepMS)
	}
	needed = min(needed, ExclusiveCaptureBufferMaxMS)
	if needed > state.ms {
		previous := state.ms
		state.ms = needed
		state.calmSince = now
		log.Printf("exclusive capture buffer grown: from_ms=%d to_ms=%d lateness=%s lost=%t", previous, needed, lateness.Round(time.Millisecond), lost)
		return needed, true
	}
	smaller := state.ms - ExclusiveCaptureBufferStepMS
	if needed > smaller {
		// The smaller buffer would not keep the reserve: stay calm-checking from now.
		state.calmSince = now
		return 0, false
	}
	if smaller < ExclusiveCaptureBufferMinMS || now.Sub(state.calmSince) < exclusiveBufferShrinkAfter {
		return 0, false
	}
	state.ms = smaller
	state.calmSince = now
	log.Printf("exclusive capture buffer shrunk: from_ms=%d to_ms=%d", smaller+ExclusiveCaptureBufferStepMS, smaller)
	return smaller, true
}

func roundUpBufferStep(d time.Duration) int {
	step := time.Duration(ExclusiveCaptureBufferStepMS) * time.Millisecond
	return int((d+step-1)/step) * ExclusiveCaptureBufferStepMS
}
