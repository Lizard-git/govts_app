package audio

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ebitengine/oto/v3"
)

type OtoPlayer struct {
	config CodecConfig
	ctx    *oto.Context
	reader *io.PipeReader
	writer *io.PipeWriter
	player *oto.Player
}

func NewOtoPlayer(config CodecConfig) (*OtoPlayer, error) {
	options := &oto.NewContextOptions{
		SampleRate:   config.SampleRate,
		ChannelCount: config.Channels,
		Format:       oto.FormatSignedInt16LE,
	}

	ctx, readyCh, err := oto.NewContext(options)
	if err != nil {
		return nil, err
	}

	select {
	case <-readyCh:
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("initialize Oto: %w", err)
		}
	case <-time.After(5 * time.Second):
		return nil, errors.New("Oto initialization timeout")
	}

	reader, writer := io.Pipe()

	player := ctx.NewPlayer(reader)
	player.SetBufferSize(config.SamplesPerFrame * config.Channels * 2)
	go player.Play()

	return &OtoPlayer{
		config: config,
		ctx:    ctx,
		reader: reader,
		writer: writer,
		player: player,
	}, nil
}

func (p *OtoPlayer) Write(samples []int16) error {
	data := int16ToBytes(samples)

	_, err := p.writer.Write(data)
	return err
}

func (p *OtoPlayer) Close() error {
	if err := p.writer.Close(); err != nil {
		return err
	}
	if err := p.reader.Close(); err != nil {
		return err
	}
	return nil
}

var _ Player = (*OtoPlayer)(nil)
