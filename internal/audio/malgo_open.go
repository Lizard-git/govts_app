package audio

import (
	"fmt"
	"log"

	"github.com/gen2brain/malgo"
)

func openMalgoStream(kind malgo.DeviceType, codec CodecConfig, deviceID string, callbacks malgo.DeviceCallbacks) (*malgo.AllocatedContext, *malgo.Device, error) {
	var context *malgo.AllocatedContext
	var device *malgo.Device
	var err error
	withAudioThread(func() {
		context, device, err = openMalgoStreamOnThread(kind, codec, deviceID, callbacks)
	})
	return context, device, err
}

func openMalgoStreamOnThread(kind malgo.DeviceType, codec CodecConfig, deviceID string, callbacks malgo.DeviceCallbacks) (*malgo.AllocatedContext, *malgo.Device, error) {
	context, device, err := startMalgoDevice(kind, codec, deviceID, false, callbacks)
	if err == nil {
		return context, device, nil
	}
	first := err
	log.Printf("audio device open failed: kind=%d device=%q error=%v", kind, deviceID, err)

	context, device, err = startMalgoDevice(kind, codec, deviceID, true, callbacks)
	if err == nil {
		return context, device, nil
	}
	log.Printf("audio device native-format open failed: kind=%d device=%q error=%v", kind, deviceID, err)

	// Remote Desktop exposes "Remote Audio" as the system default. Opening that
	// endpoint by id is rejected, while the default endpoint plays through RDP.
	if deviceID != "" && deviceIsDefault(kind, deviceID) {
		context, device, err = startMalgoDevice(kind, codec, "", false, callbacks)
		if err == nil {
			return context, device, nil
		}
		log.Printf("default audio device open failed: kind=%d error=%v", kind, err)
		context, device, err = startMalgoDevice(kind, codec, "", true, callbacks)
		if err == nil {
			return context, device, nil
		}
		log.Printf("default audio device native-format open failed: kind=%d error=%v", kind, err)
	}
	return nil, nil, first
}

func startMalgoDevice(kind malgo.DeviceType, codec CodecConfig, deviceID string, native bool, callbacks malgo.DeviceCallbacks) (*malgo.AllocatedContext, *malgo.Device, error) {
	label := "playback"
	if kind == malgo.Capture {
		label = "capture"
	}
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize %s context: %w", label, err)
	}
	fail := func(err error) (*malgo.AllocatedContext, *malgo.Device, error) {
		_ = context.Uninit()
		context.Free()
		return nil, nil, err
	}

	deviceConfig := malgo.DefaultDeviceConfig(kind)
	target := &deviceConfig.Playback
	if kind == malgo.Capture {
		target = &deviceConfig.Capture
	}
	target.Format = malgo.FormatS16
	target.Channels = uint32(codec.Channels)
	target.ShareMode = malgo.Shared
	deviceConfig.SampleRate = uint32(codec.SampleRate)
	if native {
		// Let miniaudio convert 48 kHz mono into the mix format WASAPI already
		// accepted. Remote Desktop audio rejects AUTOCONVERTPCM and hardware offload.
		deviceConfig.Wasapi.NoAutoConvertSRC = 1
		deviceConfig.Wasapi.NoDefaultQualitySRC = 1
		deviceConfig.Wasapi.NoHardwareOffloading = 1
		deviceConfig.PerformanceProfile = malgo.Conservative
		deviceConfig.PeriodSizeInFrames = 0
		deviceConfig.PeriodSizeInMilliseconds = 20
	}
	releaseDeviceID, err := configureDeviceID(&target.DeviceID, deviceID)
	if err != nil {
		return fail(err)
	}
	defer releaseDeviceID()

	device, err := malgo.InitDevice(context.Context, deviceConfig, callbacks)
	if err != nil {
		return fail(fmt.Errorf("initialize %s device: %w", label, err))
	}
	if err := device.Start(); err != nil {
		device.Uninit()
		return fail(fmt.Errorf("start %s device: %w", label, err))
	}
	return context, device, nil
}

func deviceIsDefault(kind malgo.DeviceType, id string) bool {
	if id == "" {
		return false
	}
	devices, err := listDevicesOnThread()
	if err != nil {
		log.Printf("audio device default check failed: %v", err)
		return false
	}
	list := devices.Playback
	if kind == malgo.Capture {
		list = devices.Capture
	}
	for _, device := range list {
		if device.ID == id && device.IsDefault {
			return true
		}
	}
	return false
}
