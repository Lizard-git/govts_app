package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"example.com/go-voice-mvp/internal/audio"
)

type DecoderFactory func() (audio.Decoder, error)

var errInvalidAudioFrame = errors.New("invalid audio frame")

type streamDecoder struct {
	decoder  audio.Decoder
	lastSeen time.Time
}

type streamDecoders struct {
	newDecoder DecoderFactory
	bySender   map[uint64]streamDecoder
}

func newStreamDecoders(newDecoder DecoderFactory) *streamDecoders {
	return &streamDecoders{
		newDecoder: newDecoder,
		bySender:   make(map[uint64]streamDecoder),
	}
}

func (d *streamDecoders) decode(frame audio.MediaFrame) (audio.MediaPCMFrame, error) {
	return d.decodeAt(frame, time.Now())
}

func (d *streamDecoders) decodeAt(
	frame audio.MediaFrame,
	now time.Time,
) (audio.MediaPCMFrame, error) {
	stream, ok := d.bySender[frame.SenderID]
	if !ok {
		decoder, err := d.newDecoder()
		if err != nil {
			return audio.MediaPCMFrame{}, fmt.Errorf(
				"create decoder for sender %d: %w",
				frame.SenderID,
				err,
			)
		}
		stream.decoder = decoder
	}
	stream.lastSeen = now
	d.bySender[frame.SenderID] = stream

	samples, err := stream.decoder.Decode(frame.Data)
	if err != nil {
		// A malformed frame may leave codec state partially updated. Reset only
		// this sender; the next frame will get a fresh decoder.
		delete(d.bySender, frame.SenderID)
		return audio.MediaPCMFrame{}, fmt.Errorf(
			"%w: decode frame from sender %d: %w",
			errInvalidAudioFrame,
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

func (d *streamDecoders) RemoveInactive(now time.Time, timeout time.Duration) int {
	removed := 0
	for senderID, stream := range d.bySender {
		if now.Sub(stream.lastSeen) < timeout {
			continue
		}
		delete(d.bySender, senderID)
		removed++
	}
	return removed
}

func DecodeLoop(
	ctx context.Context,
	newDecoder DecoderFactory,
	encodedCh <-chan audio.MediaFrame,
	pcmOutCh chan<- audio.MediaPCMFrame,
) error {
	defer close(pcmOutCh)
	decoders := newStreamDecoders(newDecoder)
	cleanupTicker := time.NewTicker(streamCleanupInterval)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-cleanupTicker.C:
			decoders.RemoveInactive(now, DefaultStreamIdleTimeout)

		case frame, ok := <-encodedCh:
			if !ok {
				return nil
			}

			pcmFrame, err := decoders.decode(frame)
			if errors.Is(err, errInvalidAudioFrame) {
				continue
			}
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
