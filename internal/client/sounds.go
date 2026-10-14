package client

import (
	"math"
	"time"

	"uniclog.io/sonoryx/internal/audio"
)

const (
	SoundPackShort  = "short"
	SoundPackSoft   = "soft"
	SoundPackLow    = "low"
	SoundPackDouble = "double"
	SoundPackSignal = "signal"

	DefaultSoundPack   = SoundPackSoft
	DefaultSoundVolume = float32(0.8)
)

type soundPack struct {
	id       string
	root     float64
	noteMS   int
	gapMS    int
	harmonic float64
	mix      float64
	peak     float64
}

var soundPacks = []soundPack{
	{id: SoundPackShort, root: 988, noteMS: 40, gapMS: 0, peak: 5600},
	{id: SoundPackSoft, root: 520, noteMS: 60, gapMS: 0, peak: 5200},
	{id: SoundPackLow, root: 280, noteMS: 140, gapMS: 30, harmonic: 2, mix: 0.4, peak: 6400},
	{id: SoundPackDouble, root: 440, noteMS: 70, gapMS: 46, peak: 5600},
	{id: SoundPackSignal, root: 784, noteMS: 100, gapMS: 20, harmonic: 3, mix: 0.25, peak: 6000},
}

func NormalizeSoundPack(id string) (string, bool) {
	for _, pack := range soundPacks {
		if pack.id == id {
			return id, true
		}
	}
	return "", false
}

func packByID(id string) soundPack {
	for _, pack := range soundPacks {
		if pack.id == id {
			return pack
		}
	}
	for _, pack := range soundPacks {
		if pack.id == DefaultSoundPack {
			return pack
		}
	}
	return soundPacks[0]
}

type cueNote struct {
	ratio float64
	ms    int
	amp   float64
	gap   int
}

func (p soundPack) notes(kind NotificationSoundKind) []cueNote {
	n, gap := p.noteMS, p.gapMS
	if n < 20 {
		n = 20
	}
	switch kind {
	case NotificationJoined:
		return []cueNote{{1, n, 1, gap}, {700.0 / 520.0, n, 1, 0}}
	case NotificationLeft:
		return []cueNote{{620.0 / 520.0, n, 1, gap}, {420.0 / 520.0, n, 1, 0}}
	case NotificationChannelChanged:
		return []cueNote{{1.12, n * 4 / 3, 0.9, 0}}
	case NotificationMessageSent:
		return []cueNote{{1.55, n, 0.9, gap}, {1.9, n, 0.85, 0}}
	case NotificationMessageReceived:
		ms := n * 2 / 3
		if ms < 28 {
			ms = 28
		}
		return []cueNote{{1.4, ms, 0.55, gap}, {1.8, ms, 0.55, 0}}
	case NotificationScreenStarted:
		return []cueNote{{1.5, n, 1, gap}, {2, n, 1, 0}}
	case NotificationScreenStopped:
		return []cueNote{{1.85, n, 0.9, gap}, {1.15, n, 0.8, 0}}
	case NotificationAway:
		return []cueNote{{1, n, 0.9, gap}, {0.89, n, 0.85, gap}, {0.75, n * 6 / 5, 0.8, 0}}
	case NotificationReturned:
		return []cueNote{{0.84, n * 3 / 4, 0.8, gap}, {1.06, n * 3 / 4, 0.9, gap}, {1.33, n, 1, 0}}
	case NotificationWake:
		g := gap
		if g < 80 {
			g = 80
		}
		w := n * 3 / 2
		if w < 70 {
			w = 70
		}
		return []cueNote{{1.5, w, 2.2, g}, {1.5, w, 2.2, g}, {1.5, w, 2.2, g}, {1.5, w, 2.2, 0}}
	default:
		return []cueNote{{1, n, 1, 0}}
	}
}

func PreviewSoundKinds() []NotificationSoundKind {
	return []NotificationSoundKind{
		NotificationJoined,
		NotificationLeft,
		NotificationChannelChanged,
		NotificationMessageSent,
		NotificationMessageReceived,
		NotificationScreenStarted,
		NotificationScreenStopped,
		NotificationWake,
		NotificationAway,
		NotificationReturned,
	}
}

func RenderNotificationPCM(pack string, volume float32, kind NotificationSoundKind) []int16 {
	frames := notificationFrames(NotificationSound{Kind: kind, Pack: pack, Volume: volume})
	total := 0
	for _, frame := range frames {
		total += len(frame.Samples)
	}
	samples := make([]int16, 0, total)
	for _, frame := range frames {
		samples = append(samples, frame.Samples...)
	}
	return samples
}

func notificationFrames(sound NotificationSound) []audio.MediaPCMFrame {
	const (
		sampleRate   = 48000
		frameSamples = sampleRate * 20 / 1000
	)
	pack := packByID(sound.Pack)
	notes := pack.notes(sound.Kind)
	volume := float64(sound.Volume)
	if volume < 0 {
		volume = 0
	}
	if volume > 1 {
		volume = 1
	}
	total := 0
	for i, note := range notes {
		total += note.ms * sampleRate / 1000
		if i+1 < len(notes) {
			total += note.gap * sampleRate / 1000
		}
	}
	if total == 0 {
		return nil
	}
	samples := make([]int16, total)
	offset := 0
	for i, note := range notes {
		count := note.ms * sampleRate / 1000
		if count > 0 {
			hz := pack.root * note.ratio
			peak := pack.peak * note.amp * volume
			for s := 0; s < count; s++ {
				envelope := math.Sin(math.Pi * float64(s) / float64(count))
				wave := math.Sin(2 * math.Pi * hz * float64(s) / sampleRate)
				if pack.harmonic > 1 && pack.mix > 0 {
					wave += pack.mix * math.Sin(2*math.Pi*hz*pack.harmonic*float64(s)/sampleRate)
					wave /= 1 + pack.mix
				}
				samples[offset+s] = clampInt16(int64(wave * envelope * peak))
			}
			offset += count
		}
		if i+1 < len(notes) {
			offset += note.gap * sampleRate / 1000
		}
	}
	frames := make([]audio.MediaPCMFrame, 0, (len(samples)+frameSamples-1)/frameSamples)
	for start := 0; start < len(samples); start += frameSamples {
		chunk := make([]int16, frameSamples)
		copy(chunk, samples[start:min(start+frameSamples, len(samples))])
		frames = append(frames, audio.MediaPCMFrame{PlaybackEpoch: sound.PlaybackEpoch, SenderID: 0, Samples: chunk, Duration: 20 * time.Millisecond})
	}
	return frames
}
