package clientsettings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const version = 1

// Settings contains desktop client preferences that survive application restarts.
type Settings struct {
	DisplayName        string            `json:"displayName"`
	CaptureDeviceID    string            `json:"captureDeviceId"`
	PlaybackDeviceID   string            `json:"playbackDeviceId"`
	Deafened           bool              `json:"deafened"`
	RNNoiseEnabled     bool              `json:"rnnoiseEnabled"`
	RNNoiseSensitivity float32           `json:"rnnoiseSensitivity"`
	MicrophoneGain     float32           `json:"microphoneGain"`
	VADEnabled         bool              `json:"vadEnabled"`
	VADMode            string            `json:"vadMode"`
	VADSensitivity     float32           `json:"vadSensitivity"`
	TrustedMediaKeys   map[string]string `json:"trustedMediaKeys,omitempty"`
	Theme              string            `json:"theme,omitempty"`
	// Locale is the interface language: 2–8 lowercase letters, for example ru, en, ky.
	// Empty means Russian. Unknown values are kept only if they match that shape,
	// so a new language does not require a settings change. Version stays 1.
	Locale        string         `json:"locale,omitempty"`
	CloseToTray   bool           `json:"closeToTray"`
	RecentServers []RecentServer `json:"recentServers,omitempty"`
}

type RecentServer struct {
	Address     string `json:"address"`
	Alias       string `json:"alias,omitempty"`
	Favorite    bool   `json:"favorite"`
	LastVisited int64  `json:"lastVisited"`
}

const (
	// ThemeSystem is the legacy unset value. The desktop UI treats it as ThemeDark.
	ThemeSystem  = "system"
	ThemeLight   = "light"
	ThemeNeutral = "neutral"
	ThemeDark    = "dark"
	ThemeBeige   = "beige"
	ThemeBrown   = "brown"
	ThemePurple  = "purple"
)

// NormalizeLocale accepts an empty value as Russian and any 2–8 letter language id.
// The boolean is false when the value is not a language id.
func NormalizeLocale(value string) (string, bool) {
	if value == "" {
		return "ru", true
	}
	if len(value) < 2 || len(value) > 8 {
		return "", false
	}
	for _, r := range value {
		if r < 'a' || r > 'z' {
			return "", false
		}
	}
	return value, true
}

func NormalizeTheme(value string) string {
	switch value {
	case ThemeLight, ThemeNeutral, ThemeDark, ThemeBeige, ThemeBrown, ThemePurple:
		return value
	default:
		return ThemeSystem
	}
}

type persistedSettings struct {
	Version int `json:"version"`
	Settings
}

// Store persists client preferences independently of any UI framework.
type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore(path string) *Store { return &Store{path: path} }

func (store *Store) Load() (Settings, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := os.ReadFile(store.path)
	if err != nil {
		return Settings{}, err
	}
	var persisted persistedSettings
	if err := json.Unmarshal(data, &persisted); err != nil {
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	if persisted.Version != version {
		return Settings{}, fmt.Errorf("unsupported settings version %d", persisted.Version)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err == nil {
		if _, exists := fields["rnnoiseSensitivity"]; !exists {
			persisted.RNNoiseSensitivity = 1
		}
		if _, exists := fields["microphoneGain"]; !exists {
			persisted.MicrophoneGain = 1
		}
	}
	persisted.Theme = NormalizeTheme(persisted.Theme)
	return persisted.Settings, nil
}

func (store *Store) Save(settings Settings) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := json.MarshalIndent(persistedSettings{Version: version, Settings: settings}, "", "  ")
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

func DefaultPath() (string, error) {
	configPath, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve application data directory: %w", err)
	}
	return filepath.Join(configPath, "Sonoryx", "settings.json"), nil
}
