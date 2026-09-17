package wailsui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"example.com/go-voice-mvp/internal/clientapp"
	"example.com/go-voice-mvp/internal/clientsettings"
	"example.com/go-voice-mvp/internal/domain"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const operationTimeout = 10 * time.Second

type validationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *validationError) Error() string { return e.Field + ": " + e.Message }

type ConnectRequest struct {
	Name           string `json:"name"`
	Server         string `json:"server"`
	InitialChannel string `json:"initialChannel"`
}

type Service struct {
	client      *clientapp.App
	settings    *clientsettings.Store
	settingsMu  sync.RWMutex
	displayName string
}

func NewService(client *clientapp.App) *Service { return &Service{client: client} }

func (s *Service) Connect(request ConnectRequest) error {
	request.Name = strings.TrimSpace(request.Name)
	request.Server = strings.TrimSpace(request.Server)
	if request.Name == "" {
		return &validationError{Field: "name", Message: "введите имя"}
	}
	if request.Server == "" {
		return &validationError{Field: "server", Message: "введите адрес сервера"}
	}
	if err := s.setDisplayName(request.Name); err != nil {
		return err
	}
	return s.client.Connect(clientapp.ConnectOptions{
		Name:           request.Name,
		Server:         request.Server,
		InitialChannel: strings.TrimSpace(request.InitialChannel),
	})
}

func (s *Service) SavedDisplayName() string {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.displayName
}

func (s *Service) Disconnect() error {
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	return s.client.Disconnect(ctx)
}

func (s *Service) JoinChannel(channelID string) error {
	id, err := parseUint64(channelID, "channelId", false)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	return s.client.JoinChannel(ctx, domain.ChannelID(id))
}

func (s *Service) Snapshot() ClientViewDTO {
	return viewDTO(s.client.Snapshot(), s.client.LastError())
}

func (s *Service) EventsAfter(sequence string) ([]ClientEventDTO, error) {
	after, err := parseUint64(sequence, "sequence", true)
	if err != nil {
		return nil, err
	}
	events := s.client.EventsAfter(after)
	result := make([]ClientEventDTO, 0, len(events))
	for _, event := range events {
		result = append(result, eventDTO(event))
	}
	return result, nil
}

func (s *Service) SetMuted(value bool) { s.client.SetMuted(value) }

func (s *Service) SetDeafened(value bool) error {
	if err := s.client.SetDeafened(value); err != nil {
		return err
	}
	return s.saveSettings()
}

func (s *Service) SetRNNoiseEnabled(value bool) error {
	s.client.SetRNNoiseEnabled(value)
	return s.saveSettings()
}

func (s *Service) SetRNNoiseSensitivity(value float32) error {
	if err := s.client.SetRNNoiseSensitivity(value); err != nil {
		return err
	}
	return s.saveSettings()
}

func (s *Service) SetVADEnabled(value bool) error {
	s.client.SetVADEnabled(value)
	return s.saveSettings()
}

func (s *Service) SetVADMode(value string) error {
	if err := s.client.SetVADMode(value); err != nil {
		return err
	}
	return s.saveSettings()
}

func (s *Service) SetVADSensitivity(value float32) error {
	if err := s.client.SetVADSensitivity(value); err != nil {
		return err
	}
	return s.saveSettings()
}

func (s *Service) AudioDevices() (AudioDevicesDTO, error) {
	devices, err := s.client.AudioDevices()
	if err != nil {
		return AudioDevicesDTO{}, err
	}
	selected := s.client.AudioDeviceSelection()
	return AudioDevicesDTO{
		Capture:          audioDeviceDTOs(devices.Capture),
		Playback:         audioDeviceDTOs(devices.Playback),
		SelectedCapture:  selected.CaptureID,
		SelectedPlayback: selected.PlaybackID,
	}, nil
}

func (s *Service) SetCaptureDevice(id string) error {
	if err := s.client.SetCaptureDevice(strings.TrimSpace(id)); err != nil {
		return err
	}
	return s.saveSettings()
}

func (s *Service) SetPlaybackDevice(id string) error {
	if err := s.client.SetPlaybackDevice(strings.TrimSpace(id)); err != nil {
		return err
	}
	return s.saveSettings()
}

func StartEventBridge(ctx context.Context, app *application.App, client *clientapp.App) func() {
	bridgeCtx, cancel := context.WithCancel(ctx)
	stateChanges, unsubscribeState := client.Subscribe(bridgeCtx)
	eventChanges, unsubscribeEvents := client.SubscribeEvents(bridgeCtx)
	go func() {
		for range stateChanges {
			app.Event.Emit("client-state-changed", true)
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second / 30)
		defer ticker.Stop()
		var lastSequence uint64
		for {
			select {
			case <-bridgeCtx.Done():
				return
			case <-ticker.C:
				sample := client.AudioMeterSnapshot()
				if sample.Sequence == lastSequence {
					continue
				}
				lastSequence = sample.Sequence
				app.Event.Emit("audio-meter", AudioMeterDTO{
					Input:       sample.Input,
					Processed:   sample.Processed,
					Transmitted: sample.Transmitted,
				})
			}
		}
	}()
	go func() {
		for range eventChanges {
			app.Event.Emit("client-event-log-changed", true)
			app.Event.Emit("client-state-changed", true)
		}
	}()
	return func() {
		cancel()
		unsubscribeState()
		unsubscribeEvents()
	}
}

func UserMessage(err error) string {
	if err == nil {
		return ""
	}
	var validation *validationError
	if errors.As(err, &validation) {
		return validation.Message
	}
	return fmt.Sprintf("%v", err)
}
