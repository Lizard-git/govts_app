package audio

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

// OtoOutput owns the single Oto context allowed during the application
// lifetime. Each network session creates its own closeable player from it.
type OtoOutput struct {
	config CodecConfig
	ctx    *oto.Context
}

type OtoPlayer struct {
	config CodecConfig
	reader *io.PipeReader
	writer *io.PipeWriter
	player *oto.Player

	closeOnce sync.Once
	closeErr  error
}

func NewOtoOutput(config CodecConfig) (*OtoOutput, error) {
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

	return &OtoOutput{config: config, ctx: ctx}, nil
}

func (o *OtoOutput) NewPlayer() (*OtoPlayer, error) {
	if o == nil || o.ctx == nil {
		return nil, errors.New("Oto output is not initialized")
	}
	reader, writer := io.Pipe()

	player := o.ctx.NewPlayer(reader)
	player.SetBufferSize(o.config.SamplesPerFrame * o.config.Channels * 2)
	go player.Play()

	return &OtoPlayer{
		config: o.config,
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
	p.closeOnce.Do(func() {
		var readerErr error
		var writerErr error

		// Closing the reader first unblocks a concurrent Write immediately.
		if p.reader != nil {
			readerErr = p.reader.Close()
		}
		if p.writer != nil {
			writerErr = p.writer.Close()
		}
		if p.player != nil {
			p.player.Pause()
			p.player.Reset()
		}

		p.closeErr = errors.Join(readerErr, writerErr)
	})

	return p.closeErr
}

var _ Player = (*OtoPlayer)(nil)
