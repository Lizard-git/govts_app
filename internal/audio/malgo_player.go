package audio

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/gen2brain/malgo"
)

type MalgoPlayer struct {
	context *malgo.AllocatedContext
	device  *malgo.Device
	buffer  *playbackBuffer

	mu       sync.Mutex
	closed   bool
	deafened bool
}

func NewMalgoPlayer(config CodecConfig, deviceID string) (*MalgoPlayer, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("initialize playback context: %w", err)
	}
	fail := func(err error) (*MalgoPlayer, error) {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, err
	}

	buffer := &playbackBuffer{limit: config.SamplesPerFrame * config.Channels * 2 * 5}
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = uint32(config.Channels)
	deviceConfig.SampleRate = uint32(config.SampleRate)
	releaseDeviceID, err := configureDeviceID(&deviceConfig.Playback.DeviceID, deviceID)
	if err != nil {
		return fail(err)
	}
	defer releaseDeviceID()
	callbacks := malgo.DeviceCallbacks{Data: func(output, _ []byte, _ uint32) {
		buffer.read(output)
	}}
	device, err := malgo.InitDevice(ctx.Context, deviceConfig, callbacks)
	if err != nil {
		return fail(fmt.Errorf("initialize playback device: %w", err))
	}
	if err := device.Start(); err != nil {
		device.Uninit()
		return fail(fmt.Errorf("start playback device: %w", err))
	}
	return &MalgoPlayer{context: ctx, device: device, buffer: buffer}, nil
}

func (p *MalgoPlayer) Write(samples []int16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return io.ErrClosedPipe
	}
	if !p.deafened {
		p.buffer.push(int16ToBytes(samples))
	}
	return nil
}

func (p *MalgoPlayer) SetDeafened(value bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return io.ErrClosedPipe
	}
	p.deafened = value
	if value {
		p.buffer.clear()
	}
	return nil
}

func (p *MalgoPlayer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	p.buffer.clear()
	var stopErr, contextErr error
	if p.device != nil {
		if err := p.device.Stop(); err != nil {
			stopErr = fmt.Errorf("stop playback device: %w", err)
		}
		p.device.Uninit()
	}
	if p.context != nil {
		if err := p.context.Uninit(); err != nil {
			contextErr = fmt.Errorf("uninitialize playback context: %w", err)
		}
		p.context.Free()
	}
	return errors.Join(stopErr, contextErr)
}

var _ DeafenPlayer = (*MalgoPlayer)(nil)
