package voiceflow

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"uniclog.io/sonoryx/internal/protocol"
)

func sampleFrame(sequence uint32, size int) protocol.VoiceBundleFrame {
	return protocol.VoiceBundleFrame{Sequence: sequence, Payload: bytes.Repeat([]byte{byte(sequence)}, size)}
}

func TestWindowRepairsDeduplicatesAndRejectsConflictsAtomically(t *testing.T) {
	var w Window
	now := time.Unix(100, 0)
	_, _ = w.Accept([]protocol.VoiceBundleFrame{sampleFrame(1, 3)}, now)
	_, _ = w.Accept([]protocol.VoiceBundleFrame{sampleFrame(3, 3)}, now.Add(40*time.Millisecond))
	conflict := sampleFrame(3, 4)
	if _, err := w.Accept([]protocol.VoiceBundleFrame{sampleFrame(2, 3), conflict}, now.Add(50*time.Millisecond)); !errors.Is(err, ErrFrameConflict) {
		t.Fatalf("conflicting bundle: %v", err)
	}
	added, err := w.Accept([]protocol.VoiceBundleFrame{sampleFrame(2, 3), sampleFrame(3, 3)}, now.Add(60*time.Millisecond))
	if err != nil || len(added) != 1 || added[0].Sequence != 2 || w.newest != 3 {
		t.Fatalf("repair after conflict: %+v, %v", added, err)
	}
	added, err = w.Accept([]protocol.VoiceBundleFrame{sampleFrame(2, 3), sampleFrame(3, 3)}, now.Add(70*time.Millisecond))
	if err != nil || len(added) != 0 {
		t.Fatalf("duplicate replay: %+v, %v", added, err)
	}
}

func TestWindowAgeWrapSizeAndBoundedMemory(t *testing.T) {
	var w Window
	now := time.Unix(100, 0)
	_, _ = w.Accept([]protocol.VoiceBundleFrame{sampleFrame(math.MaxUint32, 600)}, now)
	primary := sampleFrame(0, 600)
	if _, err := w.Accept([]protocol.VoiceBundleFrame{primary}, now.Add(20*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if frames := w.ForFrame(primary, 3, now.Add(20*time.Millisecond)); len(frames) != 1 {
		t.Fatal("oversized copy retained")
	}
	_, _ = w.Accept([]protocol.VoiceBundleFrame{sampleFrame(2, 3)}, now.Add(40*time.Millisecond))
	if frames, err := w.Accept([]protocol.VoiceBundleFrame{sampleFrame(1, 3)}, now.Add(121*time.Millisecond)); err != nil || len(frames) != 0 {
		t.Fatal("expired repair accepted", err)
	}
	if frames := w.ForFrame(sampleFrame(3, 3), 3, now.Add(time.Second)); len(frames) != 1 {
		t.Fatal("expired history copied")
	}
	if _, err := w.Accept([]protocol.VoiceBundleFrame{sampleFrame(100000, 3)}, now.Add(time.Second)); !errors.Is(err, ErrSequenceJump) {
		t.Fatal("unbounded timeline jump accepted", err)
	}
	for i := uint32(3); i < 1000; i++ {
		if _, err := w.Accept([]protocol.VoiceBundleFrame{sampleFrame(i, 3)}, now.Add(time.Duration(i)*20*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	retained := 0
	for _, s := range w.slots {
		if s.payload != nil {
			retained++
		}
	}
	if retained > windowSize {
		t.Fatal("history grew")
	}
}

func TestWindowCopiesAreImmutableAndOldestDroppedFirst(t *testing.T) {
	var w Window
	now := time.Unix(100, 0)
	f := sampleFrame(1, 390)
	_, _ = w.Accept([]protocol.VoiceBundleFrame{f}, now)
	f.Payload[0] = 99
	_, _ = w.Accept([]protocol.VoiceBundleFrame{sampleFrame(2, 390)}, now.Add(20*time.Millisecond))
	primary := sampleFrame(3, 390)
	_, _ = w.Accept([]protocol.VoiceBundleFrame{primary}, now.Add(40*time.Millisecond))
	frames := w.ForFrame(primary, 3, now.Add(40*time.Millisecond))
	if len(frames) != 2 || frames[0].Sequence != 2 || protocol.VoiceBundleV2Size(frames) > 1200 {
		t.Fatalf("size fallback: %+v", frames)
	}
	if w.slots[1].payload[0] != 1 {
		t.Fatal("history aliases input")
	}
}
