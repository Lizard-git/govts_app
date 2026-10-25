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
	late     bool
	sequence uint32
	at       time.Time
	payload  []byte
	valid    bool
}
type Window struct {
	credits          float64
	Late, Duplicates uint64
	slots            [windowSize]sample
	newest           uint32
	newestAt         time.Time
	initialized      bool
}

func (w *Window) Timely(sequence uint32, now time.Time) bool {
	s := w.slots[sequence%windowSize]
	return s.valid && !s.late && s.sequence == sequence && s.payload != nil && int32(w.newest-sequence) >= 0 && uint32(w.newest-sequence) <= 4 && now.Sub(s.at) <= HistoryAge
}

// Accept checks every conflict before mutation, then returns only newly seen,
// timely frames. Payload bytes are copied and retained in a fixed-size window.
func (w *Window) Accept(frames []protocol.VoiceBundleFrame, now time.Time) ([]protocol.VoiceBundleFrame, error) {
	if len(frames) == 0 || len(frames) > protocol.MaxVoiceBundleV2Frames {
		return nil, protocol.ErrInvalidVoiceBundle
	}
	latest := frames[len(frames)-1].Sequence
	nextCredits := float64(64)
	if w.initialized && int32(latest-w.newest) > 0 {
		available := w.credits + max(0, now.Sub(w.newestAt).Seconds())*100
		if float64(uint32(latest-w.newest)) > available {
			return nil, ErrSequenceJump
		}
		nextCredits = min(64, available-float64(uint32(latest-w.newest)))
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
		w.credits = nextCredits
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
		if behind < 0 {
			continue
		}
		if behind >= windowSize {
			w.Late++
			continue
		}
		s := &w.slots[f.Sequence%windowSize]
		if s.valid && s.sequence == f.Sequence && (s.payload != nil || s.late) {
			w.Duplicates++
			continue
		}
		if behind > 4 || (s.valid && s.sequence == f.Sequence && now.Sub(s.at) > HistoryAge) {
			s.payload = bytes.Clone(f.Payload)
			s.valid = true
			s.sequence = f.Sequence
			s.late = true
			w.Late++
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
		if !s.valid || s.late || s.sequence != seq || s.payload == nil || now.Sub(s.at) > HistoryAge {
			continue
		}
		candidate := append([]protocol.VoiceBundleFrame{{Sequence: seq, Payload: s.payload}}, frames...)
		if protocol.VoiceBundleV2Size(candidate) <= protocol.MaxPayloadSize {
			frames = candidate
		}
	}
	return frames
}

func (w *Window) SizeDrops(primary protocol.VoiceBundleFrame, count int, now time.Time) uint64 {
	size := protocol.VoiceBundleV2HeaderSize + 6 + len(primary.Payload)
	selected := 1
	var drops uint64
	for back := uint32(1); back <= 4 && selected < min(count, 3); back++ {
		seq := primary.Sequence - back
		if !w.Timely(seq, now) {
			continue
		}
		candidate := w.slots[seq%windowSize]
		if size+6+len(candidate.payload) > protocol.MaxPayloadSize {
			drops++
		} else {
			size += 6 + len(candidate.payload)
			selected++
		}
	}
	return drops
}
