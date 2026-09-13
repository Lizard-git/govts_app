package audio

// PCMProcessor transforms microphone PCM before it is passed to the voice
// activity detector and encoder. Implementations may keep state between calls.
type PCMProcessor interface {
	Process(samples []int16) error
	Close() error
}

// PassthroughProcessor is the default processor. It leaves microphone samples
// unchanged and provides a stable integration point for denoisers such as
// RNNoise.
type PassthroughProcessor struct{}

func (PassthroughProcessor) Process([]int16) error { return nil }
func (PassthroughProcessor) Close() error          { return nil }
