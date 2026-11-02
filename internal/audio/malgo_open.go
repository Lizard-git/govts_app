package audio

import (
	"errors"
	"fmt"
	"log"
	"runtime"

	"github.com/gen2brain/malgo"
)

// openMalgoStream also reports whether the stream holds the endpoint in
// exclusive mode, so a device switch knows it must release it first.
func openMalgoStream(kind malgo.DeviceType, codec CodecConfig, deviceID string, callbacks malgo.DeviceCallbacks) (*malgo.AllocatedContext, *malgo.Device, bool, error) {
	var context *malgo.AllocatedContext
	var device *malgo.Device
	var exclusive bool
	var err error
	withAudioThread(func() {
		context, device, exclusive, err = openMalgoStreamOnThread(kind, codec, deviceID, callbacks)
	})
	return context, device, exclusive, err
}

func openMalgoStreamOnThread(kind malgo.DeviceType, codec CodecConfig, deviceID string, callbacks malgo.DeviceCallbacks) (*malgo.AllocatedContext, *malgo.Device, bool, error) {
	context, device, err := startMalgoDevice(kind, codec, deviceID, false, malgo.Shared, callbacks)
	if err == nil {
		return context, device, false, nil
	}
	first := err
	log.Printf("audio device open failed: kind=%d device=%q error=%v", kind, deviceID, err)

	context, device, err = startMalgoDevice(kind, codec, deviceID, true, malgo.Shared, callbacks)
	if err == nil {
		return context, device, false, nil
	}
	log.Printf("audio device native-format open failed: kind=%d device=%q error=%v", kind, deviceID, err)
	sharedErr := errors.Join(first, err)

	// Remote Desktop exposes "Remote Audio" as the system default. Opening that
	// endpoint by id is rejected, while the default endpoint plays through RDP.
	if deviceID != "" && deviceIsDefault(kind, deviceID) {
		context, device, err = startMalgoDevice(kind, codec, "", false, malgo.Shared, callbacks)
		if err == nil {
			return context, device, false, nil
		}
		log.Printf("default audio device open failed: kind=%d error=%v", kind, err)
		context, device, err = startMalgoDevice(kind, codec, "", true, malgo.Shared, callbacks)
		if err == nil {
			return context, device, false, nil
		}
		log.Printf("default audio device native-format open failed: kind=%d error=%v", kind, err)
	}
	// Some Windows capture endpoints reject even their supported mix format in
	// shared mode with E_INVALIDARG, but accept PCM in exclusive mode. Keep the
	// selected endpoint and only try this after the shared-mode workarounds fail.
	if shouldTryExclusiveCapture(runtime.GOOS, kind, sharedErr) {
		context, device, err = startMalgoDevice(kind, codec, deviceID, true, malgo.Exclusive, callbacks)
		if err == nil {
			log.Printf("capture device opened in exclusive fallback: device=%q internal_format=%d internal_channels=%d internal_rate=%d", deviceID, device.CaptureInternalFormat(), device.CaptureInternalChannels(), device.CaptureInternalSampleRate())
			return context, device, true, nil
		}
		log.Printf("capture device exclusive fallback failed: device=%q error=%v", deviceID, err)
		return nil, nil, false, errors.Join(sharedErr, fmt.Errorf("exclusive capture fallback: %w", err))
	}
	return nil, nil, false, first
}

// shouldTryExclusiveCapture limits the exclusive fallback to Windows capture
// endpoints whose shared-mode open was rejected with E_INVALIDARG. A busy or
// missing device must not be taken over exclusively.
func shouldTryExclusiveCapture(goos string, kind malgo.DeviceType, sharedErr error) bool {
	return goos == "windows" && kind == malgo.Capture && errors.Is(sharedErr, malgo.ErrInvalidArgs)
}

func startMalgoDevice(kind malgo.DeviceType, codec CodecConfig, deviceID string, native bool, shareMode malgo.ShareMode, callbacks malgo.DeviceCallbacks) (*malgo.AllocatedContext, *malgo.Device, error) {
	label := "playback"
	if kind == malgo.Capture {
		label = "capture"
	}
	var backends []malgo.Backend
	if shareMode == malgo.Exclusive {
		backends = []malgo.Backend{malgo.BackendWasapi}
	}
	context, err := malgo.InitContext(backends, malgo.ContextConfig{}, nil)
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
	target.ShareMode = shareMode
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
	if shareMode == malgo.Exclusive {
		// miniaudio sizes an exclusive buffer as period * periods and gets one
		// event per buffer. The default three periods would deliver 60 ms
		// bursts; one period keeps capture at the 20 ms frame cadence.
		deviceConfig.Periods = 1
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
