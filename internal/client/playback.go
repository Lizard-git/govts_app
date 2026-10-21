package client

import (
	"context"
	"log"

	"uniclog.io/sonoryx/internal/audio"
)

func PlaybackLoop(
	ctx context.Context,
	player audio.Player,
	pcmOutCh <-chan audio.PCMFrame,
	controls ...*AudioControlState,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame, ok := <-pcmOutCh:
			if !ok {
				return nil
			}
			written := false
			write := func() error {
				written = true
				return player.Write(frame.Samples)
			}
			var err error
			if len(controls) > 0 {
				err = controls[0].Play(frame.PlaybackEpoch, write)
			} else {
				err = write()
			}
			if err != nil {
				return err
			}
			if frame.Cue != 0 {
				logCueHandoff(player, NotificationSoundKind(frame.Cue), written)
			}
		}
	}
}

// logCueHandoff is the last step a cue can be traced in this process. A cue
// that reaches the device with a short buffer and a live callback and is still
// not heard was lost by the output driver or device.
func logCueHandoff(player audio.Player, kind NotificationSoundKind, written bool) {
	if !written {
		log.Printf("sound cue dropped: kind=%s reason=deafened_or_playback_epoch", kind)
		return
	}
	stats, ok := player.(audio.PlaybackStats)
	if !ok {
		log.Printf("sound cue to device: kind=%s", kind)
		return
	}
	buffered, sinceCallback := stats.PlaybackStats()
	log.Printf("sound cue to device: kind=%s buffered_ms=%d last_callback_ms=%d", kind, buffered.Milliseconds(), sinceCallback.Milliseconds())
}
