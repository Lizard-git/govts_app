package client

import (
	"context"
	"fmt"

	"example.com/go-voice-mvp/internal/audio"
)

type DecoderFactory func() (audio.Decoder, error)

type streamDecoders struct {
	newDecoder DecoderFactory
	bySender   map[uint64]audio.Decoder
}

func newStreamDecoders(newDecoder DecoderFactory) *streamDecoders {
	return &streamDecoders{
		newDecoder: newDecoder,
		bySender:   make(map[uint64]audio.Decoder),
	}
}

func (d *streamDecoders) decode(frame audio.MediaFrame) (audio.MediaPCMFrame, error) {
	decoder, ok := d.bySender[frame.SenderID]
	if !ok {
		var err error
		decoder, err = d.newDecoder()
		if err != nil {
			return audio.MediaPCMFrame{}, fmt.Errorf(
				"create decoder for sender %d: %w",
				frame.SenderID,
				err,
			)
		}
		d.bySender[frame.SenderID] = decoder
	}

	samples, err := decoder.Decode(frame.Data)
	if err != nil {
		return audio.MediaPCMFrame{}, fmt.Errorf(
			"decode frame from sender %d: %w",
			frame.SenderID,
			err,
		)
	}

	return audio.MediaPCMFrame{
		SenderID: frame.SenderID,
		Sequence: frame.Sequence,
		Samples:  samples,
		Duration: frame.Duration,
	}, nil
}

func DecodeLoop(
	ctx context.Context,
	newDecoder DecoderFactory,
	encodedCh <-chan audio.MediaFrame,
	pcmOutCh chan<- audio.MediaPCMFrame,
) error {
	defer close(pcmOutCh)
	decoders := newStreamDecoders(newDecoder)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case frame, ok := <-encodedCh:
			if !ok {
				return nil
			}

			pcmFrame, err := decoders.decode(frame)
			if err != nil {
				return err
			}

			select {
			case pcmOutCh <- pcmFrame:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
