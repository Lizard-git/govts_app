package wailsui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"example.com/go-voice-mvp/internal/clientsettings"
)

func (s *Service) enableSettings(path string) error {
	store := clientsettings.NewStore(path)
	s.settings = store
	settings, err := store.Load()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	s.settingsMu.Lock()
	s.displayName = settings.DisplayName
	s.settingsMu.Unlock()
	var restoreErrors []error
	if err := s.client.SetVADMode(settings.VADMode); err != nil {
		restoreErrors = append(restoreErrors, err)
	}
	if err := s.client.SetVADSensitivity(settings.VADSensitivity); err != nil {
		restoreErrors = append(restoreErrors, err)
	}
	if err := s.client.SetRNNoiseSensitivity(settings.RNNoiseSensitivity); err != nil {
		restoreErrors = append(restoreErrors, err)
	}
	s.client.SetRNNoiseEnabled(settings.RNNoiseEnabled)
	s.client.SetVADEnabled(settings.VADEnabled)
	if err := s.client.SetDeafened(settings.Deafened); err != nil {
		restoreErrors = append(restoreErrors, err)
	}
	if settings.CaptureDeviceID != "" {
		if err := s.client.SetCaptureDevice(settings.CaptureDeviceID); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("restore capture device: %w", err))
		}
	}
	if settings.PlaybackDeviceID != "" {
		if err := s.client.SetPlaybackDevice(settings.PlaybackDeviceID); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("restore playback device: %w", err))
		}
	}
	return errors.Join(restoreErrors...)
}

func (s *Service) saveSettings() error {
	if s.settings == nil {
		return nil
	}
	view := s.client.Snapshot()
	devices := s.client.AudioDeviceSelection()
	s.settingsMu.RLock()
	displayName := s.displayName
	s.settingsMu.RUnlock()
	return s.settings.Save(clientsettings.Settings{
		DisplayName:        displayName,
		CaptureDeviceID:    devices.CaptureID,
		PlaybackDeviceID:   devices.PlaybackID,
		Deafened:           view.Deafened,
		RNNoiseEnabled:     view.RNNoiseEnabled,
		RNNoiseSensitivity: view.RNNoiseSensitivity,
		VADEnabled:         view.VADEnabled,
		VADMode:            view.VADMode,
		VADSensitivity:     view.VADSensitivity,
	})
}

func (s *Service) setDisplayName(value string) error {
	s.settingsMu.Lock()
	s.displayName = strings.TrimSpace(value)
	s.settingsMu.Unlock()
	return s.saveSettings()
}

func EnableDefaultSettings(service *Service) error {
	path, err := clientsettings.DefaultPath()
	if err != nil {
		return err
	}
	return service.enableSettings(path)
}
