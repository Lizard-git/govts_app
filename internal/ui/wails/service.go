package wailsui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/go-voice-mvp/internal/clientapp"
	"example.com/go-voice-mvp/internal/clientsettings"
	"example.com/go-voice-mvp/internal/domain"
	"example.com/go-voice-mvp/internal/media"
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
	client           *clientapp.App
	settings         *clientsettings.Store
	settingsMu       sync.RWMutex
	displayName      string
	serverAddress    string
	trustedMediaKeys map[string]string
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
	s.settingsMu.Lock()
	s.serverAddress = request.Server
	s.settingsMu.Unlock()
	return s.client.Connect(clientapp.ConnectOptions{
		Name:           request.Name,
		Server:         request.Server,
		InitialChannel: strings.TrimSpace(request.InitialChannel),
	})
}

type MediaTrustDTO struct {
	Fingerprint string `json:"fingerprint"`
	Trusted     bool   `json:"trusted"`
	Known       bool   `json:"known"`
}

func (s *Service) MediaServerIdentity() (MediaTrustDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	fingerprint, err := s.client.MediaServerFingerprint(ctx)
	if err != nil {
		return MediaTrustDTO{}, err
	}
	s.settingsMu.RLock()
	trusted := s.trustedMediaKeys[s.serverAddress]
	s.settingsMu.RUnlock()
	return MediaTrustDTO{Fingerprint: fingerprint, Trusted: trusted == fingerprint, Known: trusted != ""}, nil
}

func (s *Service) TrustMediaServer(fingerprint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	actual, err := s.client.MediaServerFingerprint(ctx)
	if err != nil {
		return err
	}
	if actual != strings.TrimSpace(fingerprint) {
		return errors.New("отпечаток media-сервера изменился")
	}
	s.settingsMu.Lock()
	if s.trustedMediaKeys == nil {
		s.trustedMediaKeys = make(map[string]string)
	}
	s.trustedMediaKeys[s.serverAddress] = actual
	s.settingsMu.Unlock()
	return s.saveSettings()
}

func (s *Service) PublishScreen(offer media.SessionDescription) (clientapp.MediaPublishResult, error) {
	pin, err := s.mediaPin()
	if err != nil {
		return clientapp.MediaPublishResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return s.client.PublishScreen(ctx, offer, pin)
}

func (s *Service) SubscribeScreen(streamID, subscriberID string, offer media.SessionDescription) (clientapp.MediaSubscribeResult, error) {
	pin, err := s.mediaPin()
	if err != nil {
		return clientapp.MediaSubscribeResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return s.client.SubscribeScreen(ctx, streamID, subscriberID, offer, pin)
}

func (s *Service) StopScreen(streamID string) error {
	pin, err := s.mediaPin()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	return s.client.StopScreen(ctx, streamID, pin)
}
func (s *Service) UnsubscribeScreen(streamID, subscriberID string) error {
	pin, err := s.mediaPin()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	return s.client.UnsubscribeScreen(ctx, streamID, subscriberID, pin)
}

func (s *Service) OpenScreenWindow(streamID, ownerName string) error {
	if _, err := parseUint64(streamID, "streamId", false); err != nil {
		return err
	}
	app := application.Get()
	if app == nil {
		return errors.New("application is not ready")
	}
	name := "screen-" + streamID
	if existing, ok := app.Window.Get(name); ok {
		existing.Focus()
		return nil
	}
	ownerName = strings.TrimSpace(ownerName)
	if ownerName == "" {
		ownerName = "Участник"
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             name,
		Title:            "Демонстрация экрана — " + ownerName,
		Width:            1280,
		Height:           800,
		MinWidth:         640,
		MinHeight:        420,
		BackgroundColour: application.NewRGB(8, 12, 20),
		URL:              "/?screen=" + url.QueryEscape(streamID) + "&owner=" + url.QueryEscape(ownerName) + "&nonce=" + strconv.FormatInt(time.Now().UnixNano(), 10),
	})
	return nil
}

func (s *Service) mediaPin() (string, error) {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	pin := s.trustedMediaKeys[s.serverAddress]
	if pin == "" {
		return "", errors.New("media-сервер ещё не подтверждён")
	}
	return pin, nil
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
