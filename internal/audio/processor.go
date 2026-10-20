package audio

// PCMFilter transforms microphone PCM before it is passed to analysis and the
// encoder. Implementations may keep state between calls.
type PCMFilter interface {
	Process(samples []int16) error
	Reset() error
	Close() error
}
