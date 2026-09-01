package audio

import (
	pionopus "github.com/pion/opus"
)

const maxOpusPacketSize = 4000

type OpusEncoder struct {
	config  CodecConfig
	encoder *pionopus.Encoder
}

type OpusDecoder struct {
	config  CodecConfig
	decoder pionopus.Decoder
}

func NewOpusEncoder(config CodecConfig) (*OpusEncoder, error) {
	encoder, err := pionopus.NewEncoder(
		pionopus.WithSampleRate(config.SampleRate),
		pionopus.WithChannels(config.Channels),
	)
	if err != nil {
		return nil, err
	}

	return &OpusEncoder{
		config:  config,
		encoder: encoder,
	}, nil
}

func (e *OpusEncoder) Encode(samples []int16) ([]byte, error) {
	pcm := int16ToBytes(samples)

	packet := make([]byte, maxOpusPacketSize)

	n, err := e.encoder.Encode(pcm, packet)
	if err != nil {
		return nil, err
	}

	return packet[:n], nil
}

func NewOpusDecoder(config CodecConfig) (*OpusDecoder, error) {
	decoder, err := pionopus.NewDecoderWithOutput(
		config.SampleRate,
		config.Channels,
	)
	if err != nil {
		return nil, err
	}

	return &OpusDecoder{
		config:  config,
		decoder: decoder,
	}, nil
}

func (d *OpusDecoder) Decode(data []byte) ([]int16, error) {
	samples := make(
		[]int16,
		d.config.SamplesPerFrame*d.config.Channels,
	)

	n, err := d.decoder.DecodeToInt16(data, samples)
	if err != nil {
		return nil, err
	}

	return samples[:n*d.config.Channels], nil
}
