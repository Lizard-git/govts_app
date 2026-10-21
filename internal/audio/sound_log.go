package audio

import (
	"log"
	"sync/atomic"
)

// SoundLogFlag turns on the per-cue diagnostics in client.log.
const SoundLogFlag = "--sound-log"

var soundLog atomic.Bool

// SetSoundLog enables the sound cue trace: each cue's path from the queue to
// the output device, the state-sync steps that trigger cues and playback
// buffer overflows. It is off by default and costs one atomic load per line.
func SetSoundLog(enabled bool) { soundLog.Store(enabled) }

func SoundLogEnabled() bool { return soundLog.Load() }

func SoundLogf(format string, args ...any) {
	if soundLog.Load() {
		log.Printf(format, args...)
	}
}
