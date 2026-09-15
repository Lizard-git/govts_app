package wailsui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const settingsVersion = 1

type persistedSettings struct {
	Version          int     `json:"version"`
	DisplayName      string  `json:"displayName"`
	CaptureDeviceID  string  `json:"captureDeviceId"`
	PlaybackDeviceID string  `json:"playbackDeviceId"`
	Deafened         bool    `json:"deafened"`
	RNNoiseEnabled   bool    `json:"rnnoiseEnabled"`
	VADEnabled       bool    `json:"vadEnabled"`
	VADMode          string  `json:"vadMode"`
	VADSensitivity   float32 `json:"vadSensitivity"`
}

type settingsStore struct {
	mu   sync.Mutex
	path string
}

func (store *settingsStore) load() (persistedSettings, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := os.ReadFile(store.path)
	if err != nil {
		return persistedSettings{}, err
	}
	var settings persistedSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return persistedSettings{}, fmt.Errorf("decode settings: %w", err)
	}
	if settings.Version != settingsVersion {
		return persistedSettings{}, fmt.Errorf("unsupported settings version %d", settings.Version)
	}
	return settings, nil
}

func (store *settingsStore) save(settings persistedSettings) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	if err := os.WriteFile(store.path, data, 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

func (s *Service) enableSettings(path string) error {
	store := &settingsStore{path: path}
	s.settings = store
	settings, err := store.load()
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
	return s.settings.save(persistedSettings{
		Version:          settingsVersion,
		DisplayName:      displayName,
		CaptureDeviceID:  devices.CaptureID,
		PlaybackDeviceID: devices.PlaybackID,
		Deafened:         view.Deafened,
		RNNoiseEnabled:   view.RNNoiseEnabled,
		VADEnabled:       view.VADEnabled,
		VADMode:          view.VADMode,
		VADSensitivity:   view.VADSensitivity,
	})
}

func (s *Service) setDisplayName(value string) error {
	s.settingsMu.Lock()
	s.displayName = strings.TrimSpace(value)
	s.settingsMu.Unlock()
	return s.saveSettings()
}

func settingsPath() (string, error) {
	configPath, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve application data directory: %w", err)
	}
	return filepath.Join(configPath, "Govts", "settings.json"), nil
}

func EnableDefaultSettings(service *Service) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	return service.enableSettings(path)
}
