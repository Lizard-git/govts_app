package audio

// PCMFilter transforms microphone PCM before it is passed to analysis and the
// encoder. Implementations may keep state between calls.
type PCMFilter interface {
	Process(samples []int16) error
	Reset() error
	Close() error
}

// PassthroughFilter is the default filter. It leaves microphone samples
// unchanged and provides a stable integration point for denoisers such as
// RNNoise.
type PassthroughFilter struct{}

func (PassthroughFilter) Process([]int16) error { return nil }
func (PassthroughFilter) Reset() error          { return nil }
func (PassthroughFilter) Close() error          { return nil }
