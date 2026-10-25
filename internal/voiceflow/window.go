package voiceflow

import (
	"bytes"
	"errors"
	"time"

	"uniclog.io/sonoryx/internal/protocol"
)

const HistoryAge = 80 * time.Millisecond
const windowSize = 128

var ErrFrameConflict = errors.New("voice frame identity has conflicting payload")
var ErrSequenceJump = errors.New("voice frame sequence jump exceeds timeline")

type sample struct {
	sequence uint32
	at       time.Time
	payload  []byte
	valid    bool
}
type Window struct {
	slots       [windowSize]sample
	newest      uint32
	newestAt    time.Time
	initialized bool
}

// Accept checks every conflict before mutation, then returns only newly seen,
// timely frames. Payload bytes are copied and retained in a fixed-size window.
func (w *Window) Accept(frames []protocol.VoiceBundleFrame, now time.Time) ([]protocol.VoiceBundleFrame, error) {
	if len(frames) == 0 || len(frames) > protocol.MaxVoiceBundleV2Frames {
		return nil, protocol.ErrInvalidVoiceBundle
	}
	latest := frames[len(frames)-1].Sequence
	if w.initialized && int64(int32(latest-w.newest)) > 64+int64(now.Sub(w.newestAt)/(10*time.Millisecond)) {
		return nil, ErrSequenceJump
	}
	for i, f := range frames {
		if len(f.Payload) == 0 || len(f.Payload) > protocol.MaxVoiceFrameSize {
			return nil, protocol.ErrInvalidVoiceBundle
		}
		if i > 0 && (int32(f.Sequence-frames[i-1].Sequence) <= 0 || f.Sequence-frames[0].Sequence > protocol.MaxVoiceBundleSpan) {
			return nil, protocol.ErrInvalidVoiceBundle
		}
		s := w.slots[f.Sequence%windowSize]
		if s.valid && s.sequence == f.Sequence && s.payload != nil && !bytes.Equal(s.payload, f.Payload) {
			return nil, ErrFrameConflict
		}
	}
	if !w.initialized || int32(latest-w.newest) > 0 {
		start := latest - uint32(windowSize-1)
		if w.initialized && uint32(latest-w.newest) < windowSize {
			start = w.newest + 1
		}
		for n := start; ; n++ {
			w.slots[n%windowSize] = sample{sequence: n, at: now, valid: true}
			if n == latest {
				break
			}
		}
		w.newest, w.newestAt, w.initialized = latest, now, true
	}
	var added []protocol.VoiceBundleFrame
	for _, f := range frames {
		behind := int32(w.newest - f.Sequence)
		if behind < 0 || behind >= windowSize || behind > 4 {
			continue
		}
		s := &w.slots[f.Sequence%windowSize]
		if s.valid && s.sequence == f.Sequence && s.payload != nil {
			continue
		}
		if s.valid && s.sequence == f.Sequence && now.Sub(s.at) > HistoryAge {
			continue
		}
		at := now
		if s.valid && s.sequence == f.Sequence {
			at = s.at
		}
		*s = sample{sequence: f.Sequence, at: at, payload: bytes.Clone(f.Payload), valid: true}
		added = append(added, protocol.VoiceBundleFrame{Sequence: f.Sequence, Payload: s.payload})
	}
	return added, nil
}

// ForFrame keeps the primary and at most two previous timely copies, dropping
// the oldest copies first when the single-datagram payload limit is reached.
func (w *Window) ForFrame(primary protocol.VoiceBundleFrame, count int, now time.Time) []protocol.VoiceBundleFrame {
	frames := []protocol.VoiceBundleFrame{primary}
	for back := uint32(1); back <= 4 && len(frames) < min(count, 3); back++ {
		seq := primary.Sequence - back
		s := w.slots[seq%windowSize]
		if !s.valid || s.sequence != seq || s.payload == nil || now.Sub(s.at) > HistoryAge {
			continue
		}
		candidate := append([]protocol.VoiceBundleFrame{{Sequence: seq, Payload: s.payload}}, frames...)
		if protocol.VoiceBundleV2Size(candidate) <= protocol.MaxPayloadSize {
			frames = candidate
		}
	}
	return frames
}
