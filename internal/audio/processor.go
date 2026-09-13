package audio

// PCMProcessor transforms microphone PCM before it is passed to the voice
// activity detector and encoder. Implementations may keep state between calls.
type PCMProcessor interface {
	Process(samples []int16) (PCMAnalysis, error)
	Reset() error
	Close() error
}

type PCMAnalysis struct {
	VoiceProbability          float32
	VoiceProbabilityAvailable bool
	VoiceDetected             bool
	VoiceDetectedAvailable    bool
}

// PassthroughProcessor is the default processor. It leaves microphone samples
// unchanged and provides a stable integration point for denoisers such as
// RNNoise.
type PassthroughProcessor struct{}

func (PassthroughProcessor) Process([]int16) (PCMAnalysis, error) { return PCMAnalysis{}, nil }
func (PassthroughProcessor) Reset() error                         { return nil }
func (PassthroughProcessor) Close() error                         { return nil }
