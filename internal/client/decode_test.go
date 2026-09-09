package client

import (
	"context"
	"reflect"
	"testing"
	"time"

	"example.com/go-voice-mvp/internal/audio"
)

type decoderFunc func([]byte) ([]int16, error)

func (f decoderFunc) Decode(data []byte) ([]int16, error) {
	return f(data)
}

func TestDecodeLoopKeepsDecoderStatePerSender(t *testing.T) {
	encodedCh := make(chan audio.MediaFrame, 3)
	pcmOutCh := make(chan audio.MediaPCMFrame, 3)

	encodedCh <- audio.MediaFrame{SenderID: 10, Sequence: 1}
	encodedCh <- audio.MediaFrame{SenderID: 20, Sequence: 7}
	encodedCh <- audio.MediaFrame{SenderID: 10, Sequence: 2}
	close(encodedCh)

	created := 0
	newDecoder := func() (audio.Decoder, error) {
		created++
		decoderID := int16(created)
		calls := int16(0)

		return decoderFunc(func([]byte) ([]int16, error) {
			calls++
			return []int16{decoderID, calls}, nil
		}), nil
	}

	if err := DecodeLoop(context.Background(), newDecoder, encodedCh, pcmOutCh); err != nil {
		t.Fatal(err)
	}

	var got []audio.MediaPCMFrame
	for frame := range pcmOutCh {
		got = append(got, frame)
	}

	want := []audio.MediaPCMFrame{
		{SenderID: 10, Sequence: 1, Samples: []int16{1, 1}},
		{SenderID: 20, Sequence: 7, Samples: []int16{2, 1}},
		{SenderID: 10, Sequence: 2, Samples: []int16{1, 2}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded frames = %+v, want %+v", got, want)
	}
	if created != 2 {
		t.Fatalf("created decoders = %d, want 2", created)
	}
}

func TestStreamDecodersRemoveInactive(t *testing.T) {
	now := time.Now()
	created := 0
	decoders := newStreamDecoders(func() (audio.Decoder, error) {
		created++
		return decoderFunc(func([]byte) ([]int16, error) { return nil, nil }), nil
	})

	if _, err := decoders.decodeAt(
		audio.MediaFrame{SenderID: 10},
		now.Add(-DefaultStreamIdleTimeout),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := decoders.decodeAt(
		audio.MediaFrame{SenderID: 20},
		now.Add(-time.Second),
	); err != nil {
		t.Fatal(err)
	}

	if removed := decoders.RemoveInactive(now, DefaultStreamIdleTimeout); removed != 1 {
		t.Fatalf("RemoveInactive() = %d, want 1", removed)
	}
	if _, ok := decoders.bySender[10]; ok {
		t.Fatal("inactive decoder was not removed")
	}
	if _, ok := decoders.bySender[20]; !ok {
		t.Fatal("active decoder was removed")
	}

	if _, err := decoders.decodeAt(audio.MediaFrame{SenderID: 10}, now); err != nil {
		t.Fatal(err)
	}
	if created != 3 {
		t.Fatalf("created decoders = %d, want 3 after inactive sender returned", created)
	}
}
