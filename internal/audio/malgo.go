package audio

import (
	"encoding/binary"
	"io"

	"github.com/gen2brain/malgo"
)

type MalgoRecorder struct {
	config  CodecConfig
	context *malgo.AllocatedContext
	device  *malgo.Device

	dataCh  chan []byte
	pending []byte
}

func NewMalgoRecorder(config CodecConfig) (*MalgoRecorder, error) {
	ctx, err := malgo.InitContext(
		nil,
		malgo.ContextConfig{},
		nil,
	)
	if err != nil {
		return nil, err
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = uint32(config.Channels)
	deviceConfig.SampleRate = uint32(config.SampleRate)

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

	device, err := malgo.InitDevice(
		ctx.Context,
		deviceConfig,
		callbacks,
	)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, err
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		_ = ctx.Uninit()
		ctx.Free()
		return nil, err
	}

	return &MalgoRecorder{
		config:  config,
		context: ctx,
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
	if err := r.device.Stop(); err != nil {
		return err
	}

	r.device.Uninit()

	close(r.dataCh)

	if err := r.context.Uninit(); err != nil {
		return err
	}

	r.context.Free()

	return nil
}
