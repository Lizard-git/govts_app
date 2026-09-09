package client

import (
	"reflect"
	"testing"
	"time"

	"example.com/go-voice-mvp/internal/audio"
)

func TestMixPCMFramesSumsAndClampsSamples(t *testing.T) {
	frames := []audio.MediaPCMFrame{
		{Samples: []int16{30000, -30000, 100}, Duration: 10 * time.Millisecond},
		{Samples: []int16{10000, -10000, -50}, Duration: 20 * time.Millisecond},
	}

	got := mixPCMFrames(frames)
	want := audio.PCMFrame{
		Samples:  []int16{32767, -32768, 50},
		Duration: 20 * time.Millisecond,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mixPCMFrames() = %+v, want %+v", got, want)
	}
}

func TestPCMMixerConsumesOneFramePerSender(t *testing.T) {
	mixer := newPCMMixer()
	mixer.Push(audio.MediaPCMFrame{SenderID: 10, Samples: []int16{100, 200}})
	mixer.Push(audio.MediaPCMFrame{SenderID: 10, Samples: []int16{300, 400}})
	mixer.Push(audio.MediaPCMFrame{SenderID: 20, Samples: []int16{10, -50}})

	first, ok := mixer.Mix()
	if !ok {
		t.Fatal("first Mix() returned no frame")
	}
	if want := []int16{110, 150}; !reflect.DeepEqual(first.Samples, want) {
		t.Fatalf("first Mix() samples = %v, want %v", first.Samples, want)
	}

	second, ok := mixer.Mix()
	if !ok {
		t.Fatal("second Mix() returned no frame")
	}
	if want := []int16{300, 400}; !reflect.DeepEqual(second.Samples, want) {
		t.Fatalf("second Mix() samples = %v, want %v", second.Samples, want)
	}

	if _, ok := mixer.Mix(); ok {
		t.Fatal("third Mix() returned a frame from empty mixer")
	}
}
