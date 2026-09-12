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
	config   CodecConfig
	buffer   *playbackBuffer
	player   *oto.Player
	mu       sync.Mutex
	closed   bool
	deafened bool
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
	buffer := &playbackBuffer{limit: o.config.SamplesPerFrame * o.config.Channels * 2 * 5}

	player := o.ctx.NewPlayer(buffer)
	player.SetBufferSize(o.config.SamplesPerFrame * o.config.Channels * 2)
	player.Play()

	return &OtoPlayer{
		config: o.config,
		buffer: buffer,
		player: player,
	}, nil
}

func (p *OtoPlayer) Write(samples []int16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return io.ErrClosedPipe
	}
	if p.deafened {
		return nil
	}
	p.buffer.push(int16ToBytes(samples))
	return nil
}

func (p *OtoPlayer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	p.buffer.clear()
	return p.player.Close()
}

var _ Player = (*OtoPlayer)(nil)

func (p *OtoPlayer) SetDeafened(value bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return io.ErrClosedPipe
	}
	if p.deafened == value {
		return nil
	}
	p.deafened = value
	p.player.Pause()
	p.buffer.clear()
	p.player.Reset()
	if !value {
		p.player.Play()
	}
	return nil
}

// Oto reads silence on underrun instead of waiting on a pipe. Thus Pause/Reset
// never waits for an application writer and deafen can interrupt playback.
type playbackBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *playbackBuffer) Read(dst []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := copy(dst, b.data)
	clear(dst[n:])
	b.data = b.data[n:]
	return len(dst), nil
}
func (b *playbackBuffer) push(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(data) >= b.limit {
		b.data = append(b.data[:0], data[len(data)-b.limit:]...)
		return
	}
	if excess := len(b.data) + len(data) - b.limit; excess > 0 {
		b.data = b.data[excess:]
	}
	b.data = append(b.data, data...)
}
func (b *playbackBuffer) clear() { b.mu.Lock(); b.data = nil; b.mu.Unlock() }

var _ DeafenPlayer = (*OtoPlayer)(nil)
