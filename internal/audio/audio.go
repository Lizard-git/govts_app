package audio

import (
	"encoding/binary"
	"time"
)

type Frame struct {
	Data     []byte
	Duration time.Duration
}

type PCMFrame struct {
	Samples  []int16
	Duration time.Duration
}

type Encoder interface {
	Encode(samples []int16) ([]byte, error)
}

type Decoder interface {
	Decode(data []byte) ([]int16, error)
}

type CodecConfig struct {
	SampleRate      int
	Channels        int
	SamplesPerFrame int
}

type Player interface {
	Write(samples []int16) error
	Close() error
}

type PCM16Encoder struct {
	config CodecConfig
}

func NewPCM16Encoder(config CodecConfig) *PCM16Encoder {
	return &PCM16Encoder{
		config: config,
	}
}

func (PCM16Encoder) Encode(samples []int16) ([]byte, error) {
	return int16ToBytes(samples), nil
}

func int16ToBytes(samples []int16) []byte {
	data := make([]byte, len(samples)*2)

	for i, sample := range samples {
		binary.LittleEndian.PutUint16(
			data[i*2:i*2+2],
			uint16(sample),
		)
	}

	return data
}
