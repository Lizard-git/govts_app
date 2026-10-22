package wailsui

import (
	"errors"
	"log"

	voiceclient "uniclog.io/sonoryx/internal/client"
	"uniclog.io/sonoryx/internal/clientsettings"
	"uniclog.io/sonoryx/internal/hotkeys"
)

// HotkeyDTO is one Ctrl+Alt shortcut. Status is "active", "busy" when another
// program owns the shortcut, or "failed" when it could not be registered.
type HotkeyDTO struct {
	Action string `json:"action"`
	Key    string `json:"key"`
	Status string `json:"status"`
}

// StartHotkeys registers the saved shortcuts. They work while the window is
// hidden, so the desktop shell calls this once at startup, after settings load.
func StartHotkeys(s *Service) {
	manager, err := hotkeys.Start(s.runHotkey)
	if err != nil {
		log.Printf("start hotkeys failed: error=%v", err)
		return
	}
	s.hotkeyMu.Lock()
	s.hotkeys = manager
	s.hotkeyMu.Unlock()
	s.applyHotkeys()
}

func StopHotkeys(s *Service) {
	s.hotkeyMu.Lock()
	manager := s.hotkeys
	s.hotkeys = nil
	s.hotkeyMu.Unlock()
	if manager != nil {
		manager.Close()
	}
}

func (s *Service) Hotkeys() []HotkeyDTO {
	s.settingsMu.RLock()
	keys := cloneStringMap(s.hotkeyKeys)
	s.settingsMu.RUnlock()
	s.hotkeyMu.Lock()
	failed := s.hotkeyErrors
	s.hotkeyMu.Unlock()
	result := make([]HotkeyDTO, 0, len(clientsettings.HotkeyActions))
	for _, action := range clientsettings.HotkeyActions {
		status := "active"
		if err := failed[action]; errors.Is(err, hotkeys.ErrBusy) {
			status = "busy"
		} else if err != nil {
			status = "failed"
		}
		result = append(result, HotkeyDTO{Action: action, Key: keys[action], Status: status})
	}
	return result
}

// SetHotkey changes the key pressed with Ctrl+Alt for one action. A shortcut
// another program owns is still saved; Hotkeys reports it as busy.
func (s *Service) SetHotkey(action, key string) error {
	if !clientsettings.KnownHotkeyAction(action) {
		return &validationError{Field: "action", Message: "неизвестное действие"}
	}
	normalized, ok := clientsettings.NormalizeHotkeyKey(key)
	if !ok {
		return &validationError{Field: "key", Message: "нужна латинская буква или цифра"}
	}
	s.settingsMu.Lock()
	for other, otherKey := range s.hotkeyKeys {
		if other != action && otherKey == normalized {
			s.settingsMu.Unlock()
			return &validationError{Field: "key", Message: "клавиша уже назначена другому действию"}
		}
	}
	s.hotkeyKeys[action] = normalized
	s.settingsMu.Unlock()
	s.applyHotkeys()
	return s.saveSettings()
}

func (s *Service) applyHotkeys() {
	s.settingsMu.RLock()
	bindings := make([]hotkeys.Binding, 0, len(clientsettings.HotkeyActions))
	for _, action := range clientsettings.HotkeyActions {
		bindings = append(bindings, hotkeys.Binding{Action: action, Key: s.hotkeyKeys[action]})
	}
	s.settingsMu.RUnlock()
	s.hotkeyMu.Lock()
	defer s.hotkeyMu.Unlock()
	if s.hotkeys == nil {
		return
	}
	s.hotkeyErrors = s.hotkeys.Apply(bindings)
	for action, err := range s.hotkeyErrors {
		log.Printf("register hotkey failed: action=%s error=%v", action, err)
	}
}

// runHotkey does what the matching button in the window does, cue included.
func (s *Service) runHotkey(action string) {
	view := s.client.Snapshot()
	switch action {
	case clientsettings.HotkeyMute:
		if !view.CaptureAvailable {
			return
		}
		s.SetMuted(!view.Muted)
		if muted, _ := s.client.AudioSwitches(); muted != view.Muted {
			s.notifyHotkey(action, cueFor(muted, clientsettings.CueMicOff, clientsettings.CueMicOn))
		}
	case clientsettings.HotkeyDeafen:
		if err := s.SetDeafened(!view.Deafened); err != nil {
			log.Printf("hotkey deafen failed: error=%v", err)
			return
		}
		if _, deafened := s.client.AudioSwitches(); deafened != view.Deafened {
			s.notifyHotkey(action, cueFor(deafened, clientsettings.CueSpeakerOff, clientsettings.CueSpeakerOn))
		}
	case clientsettings.HotkeyAway:
		if view.ConnectionStatus != voiceclient.ConnectionConnected || view.ChannelID == 0 {
			return
		}
		away := false
		for _, participant := range view.Participants {
			if participant.SessionID == view.SessionID {
				away = participant.Away
				break
			}
		}
		if err := s.SetAway(!away); err != nil {
			log.Printf("hotkey away failed: away=%t error=%v", !away, err)
			return
		}
		s.notifyHotkey(action, cueFor(!away, clientsettings.CueAway, clientsettings.CueReturned))
	}
}

func cueFor(on bool, whenOn, whenOff string) string {
	if on {
		return whenOn
	}
	return whenOff
}

// hotkeyNotifier shows a system notification for a hotkey press made while
// the main window is out of sight. Window buttons and the tray never use it.
type hotkeyNotifier struct {
	hidden func() bool
	send   func(title, body string) error
}

// ConfigureHotkeyNotifications connects hotkey presses to system notifications.
// hidden reports whether the main window is minimised or in the tray.
func ConfigureHotkeyNotifications(s *Service, hidden func() bool, send func(title, body string) error) {
	s.hotkeyMu.Lock()
	s.hotkeyNotifier = &hotkeyNotifier{hidden: hidden, send: send}
	s.hotkeyMu.Unlock()
}

func (s *Service) HotkeyNotifications() bool {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.hotkeyNotifications
}

func (s *Service) SetHotkeyNotifications(value bool) error {
	s.settingsMu.Lock()
	s.hotkeyNotifications = value
	s.settingsMu.Unlock()
	return s.saveSettings()
}

// notifyHotkey reports the new state; cue names which state it is.
func (s *Service) notifyHotkey(action, cue string) {
	s.settingsMu.RLock()
	enabled := s.hotkeyNotifications
	locale := s.locale
	key := s.hotkeyKeys[action]
	s.settingsMu.RUnlock()
	s.hotkeyMu.Lock()
	notifier := s.hotkeyNotifier
	s.hotkeyMu.Unlock()
	if !enabled || notifier == nil || !notifier.hidden() {
		return
	}
	title := hotkeyNoticeTitle(locale, cue)
	// Delivery can fall back to PowerShell and take a while; the next press must not wait.
	go func() {
		if err := notifier.send(title, "Ctrl + Alt + "+key); err != nil {
			log.Printf("hotkey notification failed: action=%s error=%v", action, err)
		}
	}()
}

var hotkeyNoticeTitles = map[string]map[string]string{
	"ru": {
		clientsettings.CueMicOff:     "Микрофон выключен",
		clientsettings.CueMicOn:      "Микрофон включён",
		clientsettings.CueSpeakerOff: "Динамик выключен",
		clientsettings.CueSpeakerOn:  "Динамик включён",
		clientsettings.CueAway:       "Вы отошли",
		clientsettings.CueReturned:   "Вы на месте",
	},
	"en": {
		clientsettings.CueMicOff:     "Microphone off",
		clientsettings.CueMicOn:      "Microphone on",
		clientsettings.CueSpeakerOff: "Speaker off",
		clientsettings.CueSpeakerOn:  "Speaker on",
		clientsettings.CueAway:       "You are away",
		clientsettings.CueReturned:   "You are back",
	},
	"ky": {
		clientsettings.CueMicOff:     "Микрофон өчүрүлдү",
		clientsettings.CueMicOn:      "Микрофон күйгүзүлдү",
		clientsettings.CueSpeakerOff: "Динамик өчүрүлдү",
		clientsettings.CueSpeakerOn:  "Динамик күйгүзүлдү",
		clientsettings.CueAway:       "Сиз кеттиңиз",
		clientsettings.CueReturned:   "Сиз ордуңуздасыз",
	},
}

// hotkeyNoticeTitle falls back to Russian, the default interface language.
func hotkeyNoticeTitle(locale, cue string) string {
	if title, ok := hotkeyNoticeTitles[locale][cue]; ok {
		return title
	}
	return hotkeyNoticeTitles["ru"][cue]
}
