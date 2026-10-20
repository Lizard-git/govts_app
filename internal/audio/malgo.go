package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/gen2brain/malgo"
)

type MalgoRecorder struct {
	config  CodecConfig
	context *malgo.AllocatedContext
	device  *malgo.Device

	dataCh  chan []byte
	pending []byte

	closeOnce sync.Once
	closeErr  error
}

func NewMalgoRecorderForDevice(config CodecConfig, deviceID string) (*MalgoRecorder, error) {
	dataCh := make(chan []byte, 8)
	callbacks := malgo.DeviceCallbacks{
		Data: func(
			output []byte,
			input []byte,
			frameCount uint32,
		) {
			if len(input) == 0 {
				return
			}

			chunk := make([]byte, len(input))
			copy(chunk, input)

			select {
			case dataCh <- chunk:
			default:
				// если consumer не успевает — дропаем старый real-time chunk
			}
		},
	}

	context, device, err := openMalgoStream(malgo.Capture, config, deviceID, callbacks)
	if err != nil {
		return nil, err
	}
	return &MalgoRecorder{
		config:  config,
		context: context,
		device:  device,
		dataCh:  dataCh,
	}, nil
}

func (r *MalgoRecorder) Read(samples []int16) (int, error) {
	written := 0

	for written < len(samples) {
		if len(r.pending) < 2 {
			chunk, ok := <-r.dataCh
			if !ok {
				return written, io.EOF
			}

			r.pending = chunk
		}

		availableSamples := len(r.pending) / 2
		neededSamples := len(samples) - written

		count := min(availableSamples, neededSamples)

		for i := 0; i < count; i++ {
			samples[written+i] = int16(binary.LittleEndian.Uint16(
				r.pending[i*2 : i*2+2],
			))
		}

		consumedBytes := count * 2
		r.pending = r.pending[consumedBytes:]
		written += count
	}

	return written, nil
}

func (r *MalgoRecorder) Close() error {
	r.closeOnce.Do(func() {
		var stopErr error
		var contextErr error

		if r.device != nil {
			if err := r.device.Stop(); err != nil {
				stopErr = fmt.Errorf("stop capture device: %w", err)
			}
			// Uninit must run even when Stop fails. It also ensures that the
			// callback cannot send to dataCh after the channel is closed.
			r.device.Uninit()
		}

		if r.dataCh != nil {
			close(r.dataCh)
		}

		if r.context != nil {
			if err := r.context.Uninit(); err != nil {
				contextErr = fmt.Errorf("uninitialize audio context: %w", err)
			}
			r.context.Free()
		}

		r.closeErr = errors.Join(stopErr, contextErr)
	})

	return r.closeErr
}
