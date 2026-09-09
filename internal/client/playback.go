package client

import (
	"context"

	"example.com/go-voice-mvp/internal/audio"
)

func PlaybackLoop(
	ctx context.Context,
	player audio.Player,
	pcmOutCh <-chan audio.PCMFrame,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame, ok := <-pcmOutCh:
			if !ok {
				return nil
			}
			if err := player.Write(frame.Samples); err != nil {
				return err
			}
		}
	}
}
